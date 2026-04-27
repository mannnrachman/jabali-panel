// jabali-tetragon-relay tails /var/log/tetragon/tetragon.log
// (Tetragon's JSON event export) and POSTs each policy_match event
// as a malware ingest payload to panel-api over loopback.
//
// Runs as its own systemd unit (jabali-tetragon-relay.service) so
// the agent stays focused on UDS request/response. M33 ADR-0067.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Tetragon JSON event shapes — narrow subset of the upstream
// process_kprobe / process_exec envelope. Only the fields we route
// into malware_events are decoded; everything else is preserved
// in raw_json.
type tetragonEnvelope struct {
	NodeName       string             `json:"node_name,omitempty"`
	Time           string             `json:"time,omitempty"`
	ProcessKprobe  *processKprobe     `json:"process_kprobe,omitempty"`
	ProcessExec    *processExec       `json:"process_exec,omitempty"`
	ProcessTracing *processTracing    `json:"process_tracing,omitempty"`
	PolicyName     string             `json:"policy_name,omitempty"`
	Raw            json.RawMessage    `json:"-"`
}

type processKprobe struct {
	PolicyName string         `json:"policy_name,omitempty"`
	FunctionName string       `json:"function_name,omitempty"`
	Process    *processInfo   `json:"process,omitempty"`
	Args       []processArg   `json:"args,omitempty"`
}

type processExec struct {
	Process *processInfo `json:"process,omitempty"`
}

type processTracing struct {
	PolicyName string       `json:"policy_name,omitempty"`
	Process    *processInfo `json:"process,omitempty"`
}

type processInfo struct {
	PID        uint32 `json:"pid,omitempty"`
	UID        uint32 `json:"uid,omitempty"`
	Binary     string `json:"binary,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
}

type processArg struct {
	StringArg string `json:"string_arg,omitempty"`
	IntArg    int64  `json:"int_arg,omitempty"`
}

// Mirror of panel-api MalwareEventIngestPayload. Field names MUST
// stay in sync with panel-api/internal/api/security_malware.go.
type ingestPayload struct {
	Source     string         `json:"source"`
	EventType  string         `json:"event_type"`
	Severity   string         `json:"severity"`
	UserID     string         `json:"user_id,omitempty"`
	TargetPath string         `json:"target_path,omitempty"`
	TargetPID  uint32         `json:"target_pid,omitempty"`
	Signature  string         `json:"signature,omitempty"`
	RawJSON    map[string]any `json:"raw_json"`
	OccurredAt time.Time      `json:"occurred_at"`
}

func main() {
	var (
		logPath  = flag.String("log", "/var/log/tetragon/tetragon.log", "Tetragon JSON export log to tail")
		panelURL = flag.String("panel-url", envOr("JABALI_PANEL_API_URL", "http://127.0.0.1:8080"), "panel-api base URL (loopback)")
		level    = flag.String("log-level", envOr("JABALI_TETRAGON_RELAY_LEVEL", "info"), "log level: debug|info|warn|error")
	)
	flag.Parse()

	log := newLogger(*level)
	log.Info("jabali-tetragon-relay starting", "log_path", *logPath, "panel_url", *panelURL)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, log, *logPath, *panelURL); err != nil && ctx.Err() == nil {
		log.Error("relay exited with error", "err", err)
		os.Exit(1)
	}
}

// run reopens the log on rotation (Tetragon truncates or rotates
// based on operator config) and tails new lines forever. ctx
// cancellation aborts the next read promptly via SetReadDeadline-
// equivalent (we close the file).
func run(ctx context.Context, log *slog.Logger, logPath, panelURL string) error {
	for {
		if err := tailOnce(ctx, log, logPath, panelURL); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Warn("tail failed; retrying in 5s", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}
		// EOF without error means file was rotated; loop reopens.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

func tailOnce(ctx context.Context, log *slog.Logger, logPath, panelURL string) error {
	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	// Seek to end on first open so we don't replay the whole file
	// on relay restart. Operator who wants replay can wipe
	// /var/lib/jabali-tetragon-relay/cursor (future enhancement;
	// for now relay restart starts fresh, matching journald default).
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek end: %w", err)
	}

	reader := bufio.NewReader(f)
	for {
		if ctx.Err() != nil {
			return nil
		}
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			// No new data — sleep briefly and re-poll. Tetragon flushes
			// each event line by default, so 500ms is responsive enough.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(500 * time.Millisecond):
			}
			// Detect rotation: stat current path; if inode differs from
			// our open file, return to reopen.
			st1, err1 := os.Stat(logPath)
			st2, err2 := f.Stat()
			if err1 == nil && err2 == nil && !os.SameFile(st1, st2) {
				return nil
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		handleLine(ctx, log, panelURL, line)
	}
}

func handleLine(ctx context.Context, log *slog.Logger, panelURL, line string) {
	var env tetragonEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		log.Debug("non-JSON line skipped", "len", len(line))
		return
	}
	payload, ok := buildPayload(env, line)
	if !ok {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Warn("marshal payload", "err", err)
		return
	}
	endpoint := strings.TrimRight(panelURL, "/") + "/api/v1/admin/security/malware/event"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		log.Warn("build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "jabali-tetragon-relay/1")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Warn("post event", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Warn("panel-api rejected event", "status", resp.StatusCode, "body", string(body))
		return
	}
	log.Debug("relayed", "policy", payload.Signature, "pid", payload.TargetPID)
}

// buildPayload narrows a Tetragon envelope down to a malware ingest
// payload. Returns ok=false for envelopes we don't care about
// (process_exec without a policy match, telemetry pings, etc).
func buildPayload(env tetragonEnvelope, raw string) (ingestPayload, bool) {
	occurred := parseTime(env.Time)
	rawJSON := map[string]any{"raw": raw}

	if env.ProcessKprobe != nil {
		p := env.ProcessKprobe
		policy := p.PolicyName
		if policy == "" {
			policy = env.PolicyName
		}
		if policy == "" {
			return ingestPayload{}, false
		}
		out := ingestPayload{
			Source:     "tetragon",
			EventType:  classifyEvent(policy),
			Severity:   "critical",
			Signature:  policy,
			RawJSON:    rawJSON,
			OccurredAt: occurred,
		}
		if p.Process != nil {
			out.TargetPID = p.Process.PID
			if p.Process.Binary != "" {
				out.TargetPath = p.Process.Binary
			}
		}
		return out, true
	}
	if env.ProcessTracing != nil {
		p := env.ProcessTracing
		policy := p.PolicyName
		if policy == "" {
			return ingestPayload{}, false
		}
		out := ingestPayload{
			Source:     "tetragon",
			EventType:  classifyEvent(policy),
			Severity:   "critical",
			Signature:  policy,
			RawJSON:    rawJSON,
			OccurredAt: occurred,
		}
		if p.Process != nil {
			out.TargetPID = p.Process.PID
			out.TargetPath = p.Process.Binary
		}
		return out, true
	}
	// process_exec without a kprobe match is informational; skip to
	// avoid drowning panel-api in routine exec noise.
	return ingestPayload{}, false
}

// classifyEvent maps the policy basename onto a stable event_type
// the UI Events card filter understands.
func classifyEvent(policy string) string {
	switch policy {
	case "jabali-exec-from-tmp", "jabali-curl-bash":
		return "process_exec_alert"
	case "jabali-chmod-x-docroot":
		return "chmod_suspicious"
	case "jabali-suspicious-syscalls":
		return "suspicious_syscall"
	}
	return "process_exec_alert"
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
