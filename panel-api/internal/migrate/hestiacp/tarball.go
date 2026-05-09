package hestiacp

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

// HestiaParsedTarball indexes a v-backup-user tar. Layout:
//
//   web/<dom>/public_html/...
//   web/<dom>/private/...
//   mail/<dom>/<local>/...                  (Maildir tree)
//   db/<dbname>.sql                         (per-DB dump)
//   user.conf
//   ssh_keys                                (sometimes)
//   conf/cron/                              (varies by Hestia minor)
//
// Hestia tarballs may or may not be gzipped depending on
// /usr/local/hestia/conf/hestia.conf BACKUP_GZIP. ParseHestiaTarball
// auto-detects via the .gz magic bytes.
//
// **STATUS:** Coded against documented Hestia v-backup-user output.
// NOT validated against a live Hestia tar.
type HestiaParsedTarball struct {
	ExtractDir    string
	WebRoot       string   // <root>/web
	MailRoot      string   // <root>/mail
	MySQLDumps    []string // absolute paths to extracted db/<name>.sql files
	UserConf      string   // <root>/user.conf
	SSHKeys       string   // <root>/ssh_keys (when present)
	Skipped       []string
}

func ParseHestiaTarball(tarballPath, extractDir string) (*HestiaParsedTarball, error) {
	if tarballPath == "" || extractDir == "" {
		return nil, errors.New("ParseHestiaTarball: empty path")
	}
	if err := os.MkdirAll(extractDir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", extractDir, err)
	}
	f, err := os.Open(tarballPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", tarballPath, err)
	}
	defer f.Close()

	buf := make([]byte, 2)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, fmt.Errorf("read tarball magic: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var src io.Reader = f
	if buf[0] == 0x1f && buf[1] == 0x8b {
		gz, gerr := gzip.NewReader(f)
		if gerr != nil {
			return nil, fmt.Errorf("gunzip: %w", gerr)
		}
		defer gz.Close()
		src = gz
	}
	tr := tar.NewReader(src)
	out := &HestiaParsedTarball{ExtractDir: extractDir}

	const maxEntrySize = 100 << 30

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || filepath.IsAbs(clean) {
			out.Skipped = append(out.Skipped, "path_escape:"+hdr.Name)
			continue
		}
		dest := filepath.Join(extractDir, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o750); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", dest, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size > maxEntrySize {
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
			if _, err := io.Copy(w, io.LimitReader(tr, maxEntrySize)); err != nil {
				_ = w.Close()
				return nil, fmt.Errorf("copy %s: %w", dest, err)
			}
			if err := w.Close(); err != nil {
				return nil, fmt.Errorf("close %s: %w", dest, err)
			}
			classifyHestia(out, clean, dest)
		case tar.TypeSymlink, tar.TypeLink:
			out.Skipped = append(out.Skipped, "link:"+clean)
		default:
			out.Skipped = append(out.Skipped, fmt.Sprintf("typeflag:%c:%s", hdr.Typeflag, clean))
		}
	}
	out.WebRoot = filepath.Join(extractDir, "web")
	out.MailRoot = filepath.Join(extractDir, "mail")
	return out, nil
}

func classifyHestia(p *HestiaParsedTarball, path, abs string) {
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "db":
		if strings.HasSuffix(path, ".sql") {
			p.MySQLDumps = append(p.MySQLDumps, abs)
		}
	case "user.conf":
		p.UserConf = abs
	case "ssh_keys":
		p.SSHKeys = abs
	}
}
