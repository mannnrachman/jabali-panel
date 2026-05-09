// migration.imapsync — sync mail from any IMAP source to the
// destination Stalwart account using the imapsync binary
// (Perl, MIT-licensed, well-maintained).
//
// Use case: source is Plesk / Microsoft 365 / Google Workspace /
// any IMAP-speaking provider where the operator wants mail-only
// migration without dragging in homedir/databases/cron.
//
// imapsync is NOT auto-installed by jabali2 (operator-controlled
// dep). Returns CodeFailedPrecondition with a 'install imapsync'
// hint when missing.
//
// Wire format:
//   {
//     "job_id": "<ulid>",
//     "src":  {"host":"...", "port":993, "user":"...", "password":"..."},
//     "dest": {"email":"u@dom", "password":"..."}
//   }
//
// Source over IMAPS by default (port 993); operator can pass
// port 143 + ssl=false for legacy IMAP. Destination is always
// 127.0.0.1:993 (Stalwart IMAPS loopback) — no plaintext IMAP
// to localhost is enforced; TLS even on the loopback hop because
// Stalwart's IMAP listener requires it post-M25.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

const (
	migrationImapsyncTimeout = 6 * time.Hour
	migrationImapsyncBinary  = "imapsync"
)

type migrationImapsyncCreds struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSL      *bool  `json:"ssl,omitempty"` // nil → default true
}

type migrationImapsyncDest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type migrationImapsyncParams struct {
	JobID string                 `json:"job_id"`
	Src   migrationImapsyncCreds `json:"src"`
	Dest  migrationImapsyncDest  `json:"dest"`
}

type migrationImapsyncResult struct {
	MessagesTransferred int64 `json:"messages_transferred"`
	BytesTransferred    int64 `json:"bytes_transferred"`
	DurationSeconds     int64 `json:"duration_seconds"`
}

func init() {
	Default.Register("migration.imapsync", migrationImapsyncHandler)
}

func migrationImapsyncHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p migrationImapsyncParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "malformed JSON: " + err.Error(),
		}
	}
	if p.JobID == "" || p.Src.Host == "" || p.Src.User == "" || p.Src.Password == "" || p.Dest.Email == "" || p.Dest.Password == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "job_id, src.{host,user,password}, dest.{email,password} all required",
		}
	}
	if _, err := exec.LookPath(migrationImapsyncBinary); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeFailedPrecondition,
			Message: "imapsync binary not found in PATH; install it on the panel host " +
				"('apt-get install -y imapsync' on Debian/Ubuntu, or build from " +
				"https://imapsync.lamiral.info/ for newest release)",
		}
	}

	srcPort := p.Src.Port
	if srcPort == 0 {
		srcPort = 993
	}
	srcSSL := true
	if p.Src.SSL != nil {
		srcSSL = *p.Src.SSL
	}

	args := []string{
		"--host1", p.Src.Host,
		"--port1", strconv.Itoa(srcPort),
		"--user1", p.Src.User,
		"--password1", p.Src.Password,
		"--host2", "127.0.0.1",
		"--port2", "993",
		"--user2", p.Dest.Email,
		"--password2", p.Dest.Password,
		// Header dedup — Message-ID stable across re-runs.
		"--useheader", "Message-ID",
		// Quiet imapsync's per-message progress; the summary
		// line at the end is what we parse.
		"--no-modulesversion",
		"--noreleasecheck",
	}
	if srcSSL {
		args = append(args, "--ssl1")
	} else {
		args = append(args, "--nossl1")
	}
	// Destination is always TLS — Stalwart IMAP listener requires
	// it post-M25.
	args = append(args, "--ssl2")

	subctx, cancel := context.WithTimeout(ctx, migrationImapsyncTimeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(subctx, migrationImapsyncBinary, args...)
	out, err := cmd.CombinedOutput()
	dur := int64(time.Since(start).Seconds())
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("imapsync failed: %v: %s",
				err, truncForLogImapsync(string(out), 8192)),
		}
	}

	msgs, bytes := parseImapsyncSummary(string(out))
	return migrationImapsyncResult{
		MessagesTransferred: msgs,
		BytesTransferred:    bytes,
		DurationSeconds:     dur,
	}, nil
}

// parseImapsyncSummary scans imapsync's tail summary for the
// 'Total bytes transferred: N' + 'Messages transferred: N' lines.
// imapsync's line format is stable across versions.
var (
	imapsyncMsgsRe  = regexp.MustCompile(`(?m)^Messages transferred\s+:\s+(\d+)`)
	imapsyncBytesRe = regexp.MustCompile(`(?m)^Total bytes transferred\s+:\s+(\d+)`)
)

func parseImapsyncSummary(stdout string) (int64, int64) {
	var msgs, bytes int64
	if m := imapsyncMsgsRe.FindStringSubmatch(stdout); len(m) == 2 {
		if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			msgs = v
		}
	}
	if m := imapsyncBytesRe.FindStringSubmatch(stdout); len(m) == 2 {
		if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			bytes = v
		}
	}
	return msgs, bytes
}

// truncForLog (defined elsewhere in this package). Re-declared
// here to avoid cross-file namespace collision; see backup_home.go.
func truncForLogImapsync(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

// tearOffStrings keeps strings.Contains import live in case future
// imapsync error paths surface in stderr.
//
//nolint:unused // reserved for follow-up
func tearOffStrings() {
	_ = strings.Contains("", "")
}
