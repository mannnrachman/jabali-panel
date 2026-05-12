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

	// Email — dedicated file first (cpmove layout).
	for _, p := range []string{
		filepath.Join(extractDir, "cp", sourceUser, "contactemail"),
		filepath.Join(extractDir, "contactemail"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			meta.Email = strings.TrimSpace(string(b))
			break
		}
	}

	// Email fallback — parse userdata file for CONTACTEMAIL line.
	// cpanel's userdata is key:value (yaml-ish); we just grep the
	// line.
	if meta.Email == "" {
		for _, p := range []string{
			filepath.Join(extractDir, "cp", sourceUser),
			filepath.Join(extractDir, "userdata"),
		} {
			if email := extractKV(p, "CONTACTEMAIL"); email != "" {
				meta.Email = email
				break
			}
		}
	}

	// Shadow hash — file is a single-line shadow entry:
	//   <user>:<hash>:<lastchange>:0:99999:7:::
	for _, p := range []string{
		filepath.Join(extractDir, "cp", sourceUser, "shadow"),
		filepath.Join(extractDir, "shadow"),
	} {
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

// extractShadowHash returns the hash field of a shadow-format line
// for the named user. Format:
//
//	<user>:<hash>:<lastchange>:<min>:<max>:<warn>:<inactive>:<expire>:
func extractShadowHash(path, user string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
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
