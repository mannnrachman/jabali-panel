package commands

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// jabali manages ONLY the delimited block below in a user's
// authorized_keys — never the whole file. Any operator / break-glass
// key outside the markers is preserved across every reconcile pass.
//
// Incident 2026-05-17: the old write/delete handlers full-replaced /
// os.Remove'd the entire authorized_keys, so the reconciler's
// every-tick "user has no jabali keys → delete" wiped operator SSH
// access on every jabali host (mx/.150). Marker-scoped edits fix it.
const (
	akBeginMarker = "# >>> jabali managed (do not edit this block) >>>"
	akEndMarker   = "# <<< jabali managed <<<"
)

// stripManagedBlock returns existing with the jabali marker block (and
// its contents) removed, leaving every other line untouched.
func stripManagedBlock(existing string) string {
	if existing == "" {
		return ""
	}
	out := make([]string, 0, 8)
	inBlock := false
	for _, ln := range strings.Split(existing, "\n") {
		t := strings.TrimSpace(ln)
		if t == akBeginMarker {
			inBlock = true
			continue
		}
		if t == akEndMarker {
			inBlock = false
			continue
		}
		if inBlock {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// applyManagedBlock returns the new authorized_keys content: all
// non-jabali lines from existing preserved verbatim, followed by a
// freshly-rendered jabali block (omitted entirely when keys is empty).
// Idempotent.
func applyManagedBlock(existing string, keys []string) string {
	base := strings.TrimRight(stripManagedBlock(existing), " \t\n")

	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n")
	}
	wrote := false
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !wrote {
			b.WriteString(akBeginMarker)
			b.WriteString("\n")
			wrote = true
		}
		b.WriteString(k)
		b.WriteString("\n")
	}
	if wrote {
		b.WriteString(akEndMarker)
		b.WriteString("\n")
	}
	return b.String()
}

// writeAuthorizedKeysAtomic writes content to the user's
// authorized_keys via a temp file + atomic rename, owned by the user,
// mode 0600. Shared by the write + delete handlers.
func writeAuthorizedKeysAtomic(u *user.User, sshDir, path, content string) *agentwire.AgentError {
	if aerr := ensureSSHDir(sshDir, u); aerr != nil {
		return aerr
	}
	tmp := filepath.Join(sshDir, "authorized_keys.tmp")
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("write temp: %v", err)}
	}
	uid, gid := parseUID(u)
	if err := os.Chown(tmp, uid, gid); err != nil {
		_ = os.Remove(tmp)
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("chown temp: %v", err)}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("rename authorized_keys: %v", err)}
	}
	return nil
}

// readAuthorizedKeys returns the current file content, "" if absent.
func readAuthorizedKeys(path string) (string, *agentwire.AgentError) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read authorized_keys: %v", err)}
	}
	return string(b), nil
}
