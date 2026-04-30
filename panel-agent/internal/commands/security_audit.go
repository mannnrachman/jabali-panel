// security_audit — M39 (ADR-0085) thin shell-out to ausearch over the
// narrow auditd rules installed by install_audit_exec(). Two commands:
//
//   security.audit.recent   — most recent N events tagged jabali_susp_exec
//   security.audit.by_user  — same, filtered to a single auid (loginuid)
//
// auditd's audit.log file is the storage; we do NOT mirror events into
// MariaDB. The 11 narrow rules + auid>=1000 filter keep the volume low
// enough that grepping the log on read is cheap.

package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	osexec "os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"
)

const auditCallTimeout = 15 * time.Second

// auditEvent is the parsed shape returned to panel-api. Field set
// matches the panel-ui ExecAudit table columns; we keep the wire
// contract minimal so future ausearch flag tweaks don't ripple.
type auditEvent struct {
	Timestamp string `json:"ts"`
	Auid      int    `json:"auid"`
	Username  string `json:"username,omitempty"`
	Comm      string `json:"comm,omitempty"`
	Exe       string `json:"exe,omitempty"`
	PPID      int    `json:"ppid,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

type auditRecentRequest struct {
	Limit int `json:"limit,omitempty"`
}

type auditByUserRequest struct {
	Username string `json:"username"`
	Limit    int    `json:"limit,omitempty"`
	// Since accepts ausearch --start syntax: "recent", "today", or RFC3339.
	Since string `json:"since,omitempty"`
}

type auditEventsResponse struct {
	Events []auditEvent `json:"events"`
}

func mwAuditRecentHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req auditRecentRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, mwInvalidArg("malformed JSON body")
		}
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	events, err := runAusearch(ctx, "", "recent", limit)
	if err != nil {
		return nil, err
	}
	return auditEventsResponse{Events: events}, nil
}

func mwAuditByUserHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req auditByUserRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, mwInvalidArg("malformed JSON body")
	}
	if !validAuditUsername(req.Username) {
		return nil, mwInvalidArg("invalid username")
	}
	u, err := user.Lookup(req.Username)
	if err != nil {
		return nil, mwInvalidArg(fmt.Sprintf("unknown user: %s", req.Username))
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	since := req.Since
	if since == "" {
		since = "recent"
	}
	events, err := runAusearch(ctx, u.Uid, since, limit)
	if err != nil {
		return nil, err
	}
	return auditEventsResponse{Events: events}, nil
}

// runAusearch invokes ausearch with our narrow key + optional auid
// filter, returning the most recent `limit` events. Events older than
// `since` are filtered by ausearch itself (--start <since>).
func runAusearch(ctx context.Context, auidFilter, since string, limit int) ([]auditEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, auditCallTimeout)
	defer cancel()

	args := []string{"-k", "jabali_susp_exec", "--raw", "--start", since}
	if auidFilter != "" {
		args = append(args, "-ua", auidFilter)
	}

	cmd := osexec.CommandContext(ctx, "ausearch", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, mwInternal("ausearch stdout pipe", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, mwInternal("ausearch start", err)
	}

	events := parseAusearchRaw(stdout, limit)

	if err := cmd.Wait(); err != nil {
		// ausearch returns non-zero when no matches found — not an error.
		if exitErr, ok := err.(*osexec.ExitError); ok && exitErr.ExitCode() == 1 {
			return events, nil
		}
		return nil, mwInternal("ausearch wait", err)
	}
	return events, nil
}

func parseAusearchRaw(r io.Reader, limit int) []auditEvent {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	cache := map[int]string{}
	var events []auditEvent

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "type=SYSCALL") {
			continue
		}
		ev := parseSyscallLine(line)
		if ev.Auid > 0 {
			if u, ok := cache[ev.Auid]; ok {
				ev.Username = u
			} else if uu, err := user.LookupId(strconv.Itoa(ev.Auid)); err == nil {
				ev.Username = uu.Username
				cache[ev.Auid] = uu.Username
			}
		}
		events = append(events, ev)
	}

	// ausearch ordered oldest-first; trim from the head if oversized.
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	// Reverse so newest is first.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events
}

func parseSyscallLine(line string) auditEvent {
	ev := auditEvent{}
	if idx := strings.Index(line, "msg=audit("); idx >= 0 {
		tail := line[idx+len("msg=audit("):]
		if end := strings.Index(tail, ":"); end >= 0 {
			tsField := tail[:end]
			if dot := strings.Index(tsField, "."); dot >= 0 {
				if secs, err := strconv.ParseInt(tsField[:dot], 10, 64); err == nil {
					ev.Timestamp = time.Unix(secs, 0).UTC().Format(time.RFC3339)
				}
			}
		}
	}
	ev.Auid = parseIntField(line, "auid=")
	ev.PID = parseIntField(line, "pid=")
	ev.PPID = parseIntField(line, "ppid=")
	ev.Comm = parseQuotedField(line, "comm=")
	ev.Exe = parseQuotedField(line, "exe=")
	return ev
}

func parseIntField(line, prefix string) int {
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return 0
	}
	tail := line[idx+len(prefix):]
	end := strings.IndexAny(tail, " \t\n")
	if end < 0 {
		end = len(tail)
	}
	v, _ := strconv.Atoi(tail[:end])
	return v
}

func parseQuotedField(line, prefix string) string {
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	tail := line[idx+len(prefix):]
	if len(tail) == 0 {
		return ""
	}
	if tail[0] == '"' {
		end := strings.IndexByte(tail[1:], '"')
		if end < 0 {
			return ""
		}
		return tail[1 : 1+end]
	}
	end := strings.IndexAny(tail, " \t\n")
	if end < 0 {
		end = len(tail)
	}
	return tail[:end]
}

func validAuditUsername(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

func init() {
	Default.Register("security.audit.recent", mwAuditRecentHandler)
	Default.Register("security.audit.by_user", mwAuditByUserHandler)
}
