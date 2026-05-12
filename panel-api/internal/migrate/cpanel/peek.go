// peek.go — pre-parse helpers that read account metadata out of the
// extracted cpmove staging dir BEFORE the full pipeline runs. The
// migrate-import CLI uses these to default --target-user / email /
// password from the source instead of forcing the operator to type
// them on every run (M35.5 operator UX).

package cpanel

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// AccountMeta is the slice of cpmove-tarball state needed to auto-
// fill the panel-side user-create form. Fields are best-effort:
// missing values surface as empty strings + the caller falls back
// to operator-supplied flags or generated defaults.
type AccountMeta struct {
	// Email pulled from the source. Tries the dedicated
	// `contactemail` file first; falls back to the CONTACTEMAIL line
	// of the userdata file. Empty when neither is present.
	Email string
	// PasswordHash is the source-side crypt(3) shadow hash (typically
	// $6$SHA-512). NOT directly usable by Kratos (which expects
	// Argon2/bcrypt), but recorded for operator visibility +
	// future-side-by-side validation. Empty when shadow is absent.
	PasswordHash string
	// PrimaryDomain is the DNS= value from the cpanel userdata file
	// (the cpanel-owner's main domain). Used to construct the
	// owner-default-mailbox address (<user>@<PrimaryDomain>).
	PrimaryDomain string
}

// PeekAccountMeta scans the extracted cpmove dir for an account's
// metadata files. Tries both layouts the cpanel parser supports:
//
//	cpmove pkgacct layout: <extractDir>/cp/<user>/contactemail
//	                       <extractDir>/cp/<user>/shadow
//	full-backup layout:    <extractDir>/cp/<user>          (file)
//	                       <extractDir>/shadow             (top-level)
//
// Returns ErrNotFound only when the extract dir doesn't exist; missing
// per-field files surface as empty AccountMeta fields (caller decides
// whether to fail or fall back).
func PeekAccountMeta(extractDir, sourceUser string) (*AccountMeta, error) {
	if extractDir == "" {
		return nil, errors.New("PeekAccountMeta: extractDir empty")
	}
	if sourceUser == "" {
		return nil, errors.New("PeekAccountMeta: sourceUser empty")
	}
	if _, err := os.Stat(extractDir); err != nil {
		return nil, err
	}

	meta := &AccountMeta{}

	// Candidate root dirs. cpanel pkgacct emits several layouts in
	// the wild:
	//   cpmove-<user>/<everything>   (modern, cpanel 11.100+)
	//   cp/<user>/<everything>       (legacy cpmove)
	//   <everything>                 (full-backup wizard / pre-extracted)
	// Probe each.
	roots := []string{
		filepath.Join(extractDir, "cpmove-"+sourceUser),
		filepath.Join(extractDir, "cp", sourceUser),
		extractDir,
	}

	// Email — dedicated file first.
	for _, root := range roots {
		for _, name := range []string{"contactemail", ".contactemail"} {
			if b, err := os.ReadFile(filepath.Join(root, name)); err == nil {
				meta.Email = strings.TrimSpace(string(b))
				break
			}
		}
		if meta.Email != "" {
			break
		}
	}

	// Email + primary domain fallback — parse the userdata file for
	// CONTACTEMAIL + DNS. In modern cpmove the file is `cp/<user>`
	// (KEY=value lines).
	for _, root := range roots {
		for _, candidate := range []string{
			filepath.Join(root, "cp", sourceUser),
			filepath.Join(root, "userdata"),
			filepath.Join(root, "userdata", "main"),
		} {
			if meta.Email == "" {
				if email := extractKV(candidate, "CONTACTEMAIL"); email != "" {
					meta.Email = email
				}
			}
			if meta.PrimaryDomain == "" {
				if dom := extractKV(candidate, "DNS"); dom != "" {
					meta.PrimaryDomain = dom
				}
			}
			if meta.Email != "" && meta.PrimaryDomain != "" {
				break
			}
		}
		if meta.Email != "" && meta.PrimaryDomain != "" {
			break
		}
	}

	// Shadow hash. Two formats:
	//   - full shadow line: <user>:<hash>:<lastchange>:0:99999:7:::
	//   - bare hash:        $6$...   (cpanel's modern cpmove writes
	//                                 cpmove-<user>/shadow as the
	//                                 hash directly, no colons).
	for _, root := range roots {
		p := filepath.Join(root, "shadow")
		if hash := extractShadowHash(p, sourceUser); hash != "" {
			meta.PasswordHash = hash
			break
		}
	}

	return meta, nil
}

// extractKV reads a key:value or key=value text file + returns the
// trimmed value for key. Case-insensitive match. Empty on miss.
func extractKV(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	keyUpper := strings.ToUpper(key)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := splitKV(line)
		if !found {
			continue
		}
		if strings.EqualFold(strings.ToUpper(k), keyUpper) {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func splitKV(line string) (string, string, bool) {
	if i := strings.Index(line, ":"); i > 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
	}
	if i := strings.Index(line, "="); i > 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
	}
	return "", "", false
}

// extractShadowHash returns a usable crypt(3) hash from a shadow-
// style file. Two formats supported:
//
//	<user>:<hash>:<lastchange>:<min>:<max>:<warn>:<inactive>:<expire>:
//	<hash>     (cpanel cpmove writes the bare hash, no colons)
func extractShadowHash(path, user string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Bare hash — typical for cpanel's cpmove-<user>/shadow.
		if !strings.Contains(line, ":") && strings.HasPrefix(line, "$") {
			return line
		}
		fields := strings.SplitN(line, ":", 3)
		if len(fields) < 2 {
			continue
		}
		if fields[0] != user {
			continue
		}
		hash := fields[1]
		if hash == "" || hash == "*" || hash == "!" {
			return ""
		}
		return hash
	}
	return ""
}
