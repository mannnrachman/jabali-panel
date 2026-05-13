package commands

// migration.rsync_remote_home — rsync source:<src_path>/ → <dest_path>/
// via sshpass / ssh-key. Run by the restore stage once per domain in
// the BackupUser-emitted domains-paths.txt manifest. Replaces the
// "bundle home into cpmove tar then ImportHomeSplit" path so transfer
// resumes on transient failures + skips already-synced files.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type migrationRsyncRemoteHomeParams struct {
	JobID      string `json:"job_id"`
	Host       string `json:"host"`
	SSHUser    string `json:"ssh_user"`
	SecretPath string `json:"secret_path"` // /etc/jabali-panel/migration-secrets/<job>.env
	SrcPath    string `json:"src_path"`    // absolute source path, e.g. /home/<acct>/domains/<dom>/public_html
	DestPath   string `json:"dest_path"`   // absolute dest path on this host
	DestUser   string `json:"dest_user"`   // chown target after rsync
}

type migrationRsyncRemoteHomeResult struct {
	BytesCopied int64    `json:"bytes_copied"`
	Files       int64    `json:"files"`
	DestPath    string   `json:"dest_path"`
	Skipped     []string `json:"skipped,omitempty"`
}

func init() {
	Default.Register("migration.rsync_remote_home", migrationRsyncRemoteHomeHandler)
}

func migrationRsyncRemoteHomeHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p migrationRsyncRemoteHomeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument,
			Message: "malformed JSON: " + err.Error()}
	}
	if p.JobID == "" || p.Host == "" || p.SecretPath == "" || p.SrcPath == "" || p.DestPath == "" || p.DestUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument,
			Message: "job_id, host, secret_path, src_path, dest_path, dest_user required"}
	}
	if !looksLikeUnixUsername(p.DestUser) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument,
			Message: "invalid dest_user"}
	}
	// Refuse src/dest paths outside the per-account home convention so a
	// malformed call can't rsync arbitrary host filesystems.
	if !strings.HasPrefix(p.DestPath, "/home/") {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument,
			Message: "dest_path must be under /home/"}
	}
	if !strings.HasPrefix(p.SrcPath, "/home/") {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument,
			Message: "src_path must be under /home/ on source"}
	}
	// Secret path must live in the panel's migration-secrets dir.
	if !strings.HasPrefix(p.SecretPath, "/etc/jabali-panel/migration-secrets/") {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument,
			Message: "secret_path must live under /etc/jabali-panel/migration-secrets/"}
	}

	subctx, cancel := context.WithTimeout(ctx, 4*time.Hour)
	defer cancel()

	if mkErr := os.MkdirAll(p.DestPath, 0o755); mkErr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("mkdir %s: %v", p.DestPath, mkErr)}
	}

	sshUser := p.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}

	// Resolve SSH auth from the secret env-file. Two recognised keys
	// (same shape cpanel/da/hestia Discoverer loadSecret reads):
	//   SSH_PASSWORD=<plain>
	//   SSH_PRIVATE_KEY_B64=<base64-PEM>
	secretBytes, sErr := os.ReadFile(p.SecretPath)
	if sErr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("read secret %s: %v", p.SecretPath, sErr)}
	}
	var sshPass string
	var keyTmp string
	for _, line := range strings.Split(string(secretBytes), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "SSH_PASSWORD":
			sshPass = v
		case "SSH_PRIVATE_KEY_B64":
			// Stash the decoded key in a 0600 tempfile rsync can ssh -i.
			b64 := strings.TrimSpace(v)
			tmp, tmpErr := os.CreateTemp("", "jabali-migrate-key-*")
			if tmpErr != nil {
				continue
			}
			_ = os.Chmod(tmp.Name(), 0o600)
			// b64 decode via shell to avoid pulling another import here.
			dec := exec.CommandContext(subctx, "bash", "-c", "echo "+b64+" | base64 -d > "+tmp.Name())
			if dErr := dec.Run(); dErr != nil {
				_ = os.Remove(tmp.Name())
				continue
			}
			keyTmp = tmp.Name()
		}
	}
	if sshPass == "" && keyTmp == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition,
			Message: "no SSH_PASSWORD or SSH_PRIVATE_KEY_B64 in secret file"}
	}
	if keyTmp != "" {
		defer os.Remove(keyTmp)
	}

	// Build rsync argv. Trailing slash on source = copy contents.
	sshOpt := "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
	if keyTmp != "" {
		sshOpt += " -i " + keyTmp + " -o IdentitiesOnly=yes"
	}
	rsyncArgs := []string{
		"-aH", "--no-h", "--info=stats2",
		"--exclude=.lock", "--exclude=.cache",
		"--exclude=public_html/.well-known/acme-challenge/",
		"-e", "ssh " + sshOpt,
		sshUser + "@" + p.Host + ":" + strings.TrimRight(p.SrcPath, "/") + "/",
		strings.TrimRight(p.DestPath, "/") + "/",
	}
	var cmd *exec.Cmd
	if sshPass != "" {
		// sshpass-prefixed invocation. -e reads SSHPASS from env so the
		// password never lands in ps argv.
		cmd = exec.CommandContext(subctx, "sshpass", append([]string{"-e", "rsync"}, rsyncArgs...)...)
		cmd.Env = append(os.Environ(), "SSHPASS="+sshPass)
	} else {
		cmd = exec.CommandContext(subctx, "rsync", rsyncArgs...)
	}
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("rsync failed: %v: %s", runErr, truncate(string(out), 4096))}
	}
	bytesCopied, files := parseRsyncStats(string(out))

	// Chown to dest user + group=www-data so nginx (in www-data) can
	// traverse. Same convention migration.import_home applies.
	wwwData, lkErr := lookupGroup("www-data")
	gid := dontknowGID
	if lkErr == nil {
		gid = wwwData
	}
	uid, ulkErr := lookupUserUID(p.DestUser)
	if ulkErr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("dest_user %q not found: %v", p.DestUser, ulkErr)}
	}
	chown := exec.CommandContext(subctx, "chown", "-R",
		fmt.Sprintf("%d:%d", uid, gid), p.DestPath)
	if cout, cerr := chown.CombinedOutput(); cerr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("chown: %v: %s", cerr, truncate(string(cout), 1024))}
	}
	_ = exec.CommandContext(subctx, "find", p.DestPath, "-type", "d", "-exec", "chmod", "g+rx", "{}", "+").Run()
	_ = exec.CommandContext(subctx, "find", p.DestPath, "-type", "f", "-exec", "chmod", "g+r", "{}", "+").Run()

	return migrationRsyncRemoteHomeResult{
		BytesCopied: bytesCopied,
		Files:       files,
		DestPath:    p.DestPath,
	}, nil
}

// lookupGroup returns the GID of a unix group, sentinel on miss.
const dontknowGID = -1

func lookupGroup(name string) (int, error) {
	out, err := exec.Command("getent", "group", name).Output()
	if err != nil {
		return 0, fmt.Errorf("getent group %s: %w", name, err)
	}
	fields := bytes.Split(bytes.TrimSpace(out), []byte(":"))
	if len(fields) < 3 {
		return 0, fmt.Errorf("getent group %s: malformed", name)
	}
	var gid int
	if _, err := fmt.Sscanf(string(fields[2]), "%d", &gid); err != nil {
		return 0, err
	}
	return gid, nil
}

func lookupUserUID(name string) (int, error) {
	out, err := exec.Command("getent", "passwd", name).Output()
	if err != nil {
		return 0, fmt.Errorf("getent passwd %s: %w", name, err)
	}
	fields := bytes.Split(bytes.TrimSpace(out), []byte(":"))
	if len(fields) < 3 {
		return 0, fmt.Errorf("getent passwd %s: malformed", name)
	}
	var uid int
	if _, err := fmt.Sscanf(string(fields[2]), "%d", &uid); err != nil {
		return 0, err
	}
	return uid, nil
}

// unused-warning sink — keeps filepath dep when other paths get
// inlined later.
var _ = filepath.Join
