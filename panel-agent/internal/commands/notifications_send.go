package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// PanelAPISocketPath is the unix socket agent posts notifications.send
// envelopes to. Defaults to the M25 path (ADR-0050). Overridable via
// env JABALI_PANEL_API_SOCKET so tests + dev builds can retarget.
var PanelAPISocketPath = envOrDefault("JABALI_PANEL_API_SOCKET", "/run/jabali-panel/api.sock")

// panelAPITimeout bounds the total wall-clock for the POST to panel-api.
// Stream-publish is a single XADD so this is generous.
const panelAPITimeout = 5 * time.Second

// allowedEventKinds gates the event_kind parameter. Agent-side senders
// are limited to the events the plan defines — anything else is a bug
// and must surface at the wire boundary, not leak into Redis as a DLQ
// row.
var allowedEventKinds = map[string]struct{}{
	"cert.renew.ok":       {},
	"cert.renew.fail":     {},
	"disk.full.warn":      {},
	"disk.full.crit":      {},
	"service.down":        {},
	"crowdsec.ban.spike":  {},
	"backup.fail":         {},
}

var allowedSeverities = map[string]struct{}{
	"info":     {},
	"warning":  {},
	"error":    {},
	"critical": {},
}

type notificationsSendParams struct {
	EventKind string `json:"event_kind"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Deeplink  string `json:"deeplink,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}

type notificationsSendResult struct {
	ID string `json:"id"`
}

// panelAPIClient is a unix-socket-bound http.Client. Built once per
// process; net/http pools connections on the underlying Dialer.
var panelAPIClient = newPanelAPIClient()

func newPanelAPIClient() *http.Client {
	return &http.Client{
		Timeout: panelAPITimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", PanelAPISocketPath)
			},
		},
	}
}

func notificationsSendHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p notificationsSendParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if err := validateNotificationParams(p); err != nil {
		return nil, err
	}

	body, err := json.Marshal(p)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("marshal envelope: %v", err)}
	}
	// The POST URL host is nominal — the Transport dials the unix socket
	// regardless. Use "panel-api" so middleware logs read sanely.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://panel-api/api/v1/internal/notifications/enqueue", bytes.NewReader(body))
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := panelAPIClient.Do(req)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("panel-api post: %v", err)}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("panel-api %d: %s", resp.StatusCode, string(respBody)),
		}
	}
	var out notificationsSendResult
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &out); err != nil {
			// Panel-api returned 2xx but unexpected JSON. Still count it
			// as a successful publish — the envelope is on the stream.
			return notificationsSendResult{}, nil
		}
	}
	return out, nil
}

func validateNotificationParams(p notificationsSendParams) error {
	if _, ok := allowedEventKinds[p.EventKind]; !ok {
		return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("event_kind %q not in allowlist", p.EventKind)}
	}
	if _, ok := allowedSeverities[p.Severity]; !ok {
		return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("severity %q not in allowlist", p.Severity)}
	}
	if p.Title == "" {
		return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "title required"}
	}
	if len(p.Title) > 200 {
		return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "title must be <= 200 chars"}
	}
	if len(p.Body) > 2000 {
		return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "body must be <= 2000 chars"}
	}
	if p.Deeplink != "" {
		if _, err := url.Parse(p.Deeplink); err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("deeplink parse: %v", err)}
		}
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func init() {
	Default.Register("notifications.send", notificationsSendHandler)
}
