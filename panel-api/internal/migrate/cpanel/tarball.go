package cpanel

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ParsedTarball is the index produced by parsing a cpmove-<user>.tar.gz.
// File paths are absolute, pointing into the staging dir under
// /var/lib/jabali-migrations/<job>/extracted/. Restore-stage writers
// consume each slice (MySQLDumps → mariadb-import; ZoneFiles → pdns
// upsert; HomeDir → cp -a into /home/<target>; etc.).
type ParsedTarball struct {
	// SourceUser is the cPanel-side username taken from the tarball's
	// top-level directory (cp/<user>/...). Restore stage maps this
	// onto the operator-supplied target jabali username.
	SourceUser string
	// ExtractDir is the absolute root the entries were written under.
	ExtractDir string
	// HomeDir is .../homedir, the verbatim user home tree from the
	// source. Empty when the tarball didn't include a homedir.
	HomeDir string
	// MySQLDumps lists every per-database SQL file. cPanel layout:
	// cp/<user>/mysql/<dbname>.sql.
	MySQLDumps []string
	// ZoneFiles lists BIND-format zone files from cp/<user>/dnszones/.
	ZoneFiles []string
	// CronFiles lists per-user crontab files (cp/<user>/cron/<user>).
	CronFiles []string
	// SSHAuthorized lists authorized_keys files found under the
	// home tree (.ssh/authorized_keys). Restore upserts each line
	// after fingerprint dedup.
	SSHAuthorized []string
	// Skipped records areas the parser ignored (postgres dumps, mail
	// not in scope here, etc.). Used to populate the manifest's
	// Warnings slice.
	Skipped []string
}

// Limits parser behaviour. Tarballs from a malicious source could
// claim a 1 EB file; bound the writes so a runaway entry can't fill
// the staging volume. The cap is per-entry — total size grows with
// the source account, which is expected.
const (
	MaxEntrySize = 100 << 30 // 100 GiB per file in the tarball
)

// ParseTarball opens a cpmove-<user>.tar.gz and streams every entry
// into extractDir. Returns a populated ParsedTarball on success;
// caller is responsible for cleaning extractDir up on terminal job
// state (the restore-stage runner does this after success/failure).
func ParseTarball(tarballPath, extractDir string) (*ParsedTarball, error) {
	if tarballPath == "" || extractDir == "" {
		return nil, errors.New("ParseTarball: empty path")
	}
	if err := os.MkdirAll(extractDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", extractDir, err)
	}

	f, err := os.Open(tarballPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", tarballPath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	out := &ParsedTarball{ExtractDir: extractDir}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}

		// Refuse path-escape attempts. cpmove tarballs are well-
		// behaved but we treat them as untrusted: a compromised
		// source panel could embed `../../etc/passwd` to overwrite
		// host files outside extractDir.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || filepath.IsAbs(clean) {
			out.Skipped = append(out.Skipped, "path_escape:"+hdr.Name)
			continue
		}

		// Glean source user from top-level cp/<user>/... entry.
		if out.SourceUser == "" {
			parts := strings.Split(clean, string(filepath.Separator))
			if len(parts) >= 2 && parts[0] == "cp" {
				out.SourceUser = parts[1]
			}
		}

		dest := filepath.Join(extractDir, clean)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o750); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", dest, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size > MaxEntrySize {
				out.Skipped = append(out.Skipped, fmt.Sprintf("oversize:%s:%d", hdr.Name, hdr.Size))
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
				return nil, fmt.Errorf("mkdir parent of %s: %w", dest, err)
			}
			w, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
			if err != nil {
				return nil, fmt.Errorf("open %s: %w", dest, err)
			}
			// io.LimitReader doubles as the size enforcement —
			// even a header lying about Size can't write more
			// than MaxEntrySize bytes.
			if _, err := io.Copy(w, io.LimitReader(tr, MaxEntrySize)); err != nil {
				_ = w.Close()
				return nil, fmt.Errorf("copy %s: %w", dest, err)
			}
			if err := w.Close(); err != nil {
				return nil, fmt.Errorf("close %s: %w", dest, err)
			}
			classify(out, clean, dest)
		case tar.TypeSymlink, tar.TypeLink:
			// cPanel tarballs occasionally hard-link inside the
			// homedir (legitimate). Skip both link types in v1 —
			// restore-stage will rebuild from materialised regular
			// files. Symlinks under .ssh/, .config/ etc are
			// uncommon and the panel's own user template is the
			// source of truth on those paths.
			out.Skipped = append(out.Skipped, "link:"+clean)
		default:
			// devices, fifos, chars — never legitimate inside cp/.
			out.Skipped = append(out.Skipped, fmt.Sprintf("typeflag:%c:%s", hdr.Typeflag, clean))
		}
	}

	if out.SourceUser == "" {
		return nil, errors.New("ParseTarball: no cp/<user>/ top-level — not a cpmove tarball?")
	}
	if root := filepath.Join(extractDir, "cp", out.SourceUser, "homedir"); existsDir(root) {
		out.HomeDir = root
	}
	return out, nil
}

// classify slots a freshly-extracted file into the right area
// slice. Path is the cleaned tarball-relative path; abs is the
// on-disk extracted path.
func classify(p *ParsedTarball, path, abs string) {
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) < 3 || parts[0] != "cp" {
		return
	}
	user := parts[1]
	if user != p.SourceUser {
		// Unexpected — second account hiding in same tarball.
		// Skip + record.
		p.Skipped = append(p.Skipped, "foreign_user:"+path)
		return
	}
	rest := parts[2:]
	if len(rest) == 0 {
		return
	}
	switch rest[0] {
	case "mysql":
		if len(rest) >= 2 && strings.HasSuffix(rest[len(rest)-1], ".sql") {
			p.MySQLDumps = append(p.MySQLDumps, abs)
		}
	case "psql", "postgres":
		// ADR-0094 §5: skip PG; M37 importer integration handles.
		p.Skipped = append(p.Skipped, "postgres_unsupported:"+path)
	case "dnszones":
		if strings.HasSuffix(rest[len(rest)-1], ".db") {
			p.ZoneFiles = append(p.ZoneFiles, abs)
		}
	case "cron":
		// cPanel cron file is typically the username verbatim
		// (cp/<user>/cron/<user>). Take any non-dir entry.
		p.CronFiles = append(p.CronFiles, abs)
	case "homedir":
		// Watch for authorized_keys inside .ssh/. Only record the
		// global home/.ssh/authorized_keys; per-domain SSH dirs
		// aren't common on cPanel.
		if len(rest) >= 3 && rest[len(rest)-2] == ".ssh" && rest[len(rest)-1] == "authorized_keys" {
			p.SSHAuthorized = append(p.SSHAuthorized, abs)
		}
	}
}

func existsDir(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	return st.IsDir()
}
