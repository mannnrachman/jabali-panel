package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// Stalwart admin API coordinates. Overridable via env so tests point at an
// httptest.Server and the installer can retarget if the config bind ever
// moves off loopback.
const (
	defaultStalwartAdminURL       = "http://127.0.0.1:8446"
	defaultStalwartAdminTokenPath = "/etc/jabali-panel/stalwart-admin.token"

	envStalwartAdminURL       = "JABALI_STALWART_ADMIN_URL"
	envStalwartAdminTokenPath = "JABALI_STALWART_ADMIN_TOKEN_PATH"

	// stalwartHTTPTimeout caps the loopback-call wall time. JMAP admin
	// calls resolve in single-digit milliseconds on a healthy host; a
	// 5-second cap catches process-wedge scenarios without surprising
	// operators during a routine burst.
	stalwartHTTPTimeout = 5 * time.Second
)

// stalwartHTTPClientFunc is the injection seam for tests. Replace in
// _test.go files via a helper; restore in t.Cleanup.
var stalwartHTTPClientFunc = func() *http.Client {
	return &http.Client{Timeout: stalwartHTTPTimeout}
}

// stalwartAdminURLFunc + stalwartAdminTokenFunc let tests swap these
// without touching the process env, which is racy under t.Parallel.
var stalwartAdminURLFunc = func() string {
	if u := os.Getenv(envStalwartAdminURL); u != "" {
		return u
	}
	return defaultStalwartAdminURL
}

var stalwartAdminTokenFunc = func() (string, error) {
	path := os.Getenv(envStalwartAdminTokenPath)
	if path == "" {
		path = defaultStalwartAdminTokenPath
	}
	b, err := os.ReadFile(path) //nolint:gosec // operator-owned path; 0640 on disk
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// invalidateStalwartPrincipal tells Stalwart to drop its cached directory
// entry for the given email address so the next auth re-reads MariaDB.
//
// Design note: Stalwart re-reads the SQL directory on every auth, but
// keeps a short (default 60s) LRU cache keyed by account. After a panel-
// side mutation (row insert, password change, quota change, row delete)
// the cache must be invalidated or a user could briefly hit stale state.
// `POST /api/principal/{email}/invalidate` is the Stalwart v0.16.0 admin
// path for this; if Stalwart isn't running yet (first enable of a fresh
// domain) the call returns 503 — not an error from the panel's perspective
// because there's nothing cached to invalidate.
func invalidateStalwartPrincipal(ctx context.Context, email string) error {
	url := stalwartAdminURLFunc() + "/api/principal/" + email + "/invalidate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	token, err := stalwartAdminTokenFunc()
	if err != nil {
		return fmt.Errorf("admin token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := stalwartHTTPClientFunc().Do(req)
	if err != nil {
		// Connection-level failure — Stalwart not running or loopback
		// routing broken. The panel row is already committed, so the
		// auth will still work once Stalwart is up (directory is SQL);
		// surface as CodeUnavailable so the panel can log + retry later.
		return &agentwire.AgentError{
			Code:    agentwire.CodeUnavailable,
			Message: fmt.Sprintf("stalwart admin API unreachable: %v", err),
		}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// 404 is fine — "no cache entry" is success (happens for a fresh
	// row the agent is acking before Stalwart has seen the address).
	// 503 is fine for the same reason (Stalwart up but the SQL directory
	// hasn't been touched yet for this email).
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300,
		resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusServiceUnavailable:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "stalwart admin API rejected bearer token — admin token rotated without agent restart?",
		}
	default:
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart admin API returned %d", resp.StatusCode),
		}
	}
}

// principalQuotaResponse is the subset of Stalwart's principal-info reply
// the panel cares about. Stalwart emits more fields (roles, quotas,
// identities); we decode only what we consume.
type principalQuotaResponse struct {
	UsedBytes    uint64     `json:"usedBytes"`
	MessageCount uint64     `json:"messageCount"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
}

// getStalwartPrincipalQuota returns usage bytes + message count + last-used
// timestamp for one email address via Stalwart's admin principal info.
// Returns a zero-value struct with nil error if Stalwart reports 404 (the
// mailbox exists in SQL but has never been touched — usage genuinely zero).
func getStalwartPrincipalQuota(ctx context.Context, email string) (principalQuotaResponse, error) {
	url := stalwartAdminURLFunc() + "/api/principal/" + email
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return principalQuotaResponse{}, fmt.Errorf("build request: %w", err)
	}
	token, err := stalwartAdminTokenFunc()
	if err != nil {
		return principalQuotaResponse{}, fmt.Errorf("admin token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := stalwartHTTPClientFunc().Do(req)
	if err != nil {
		return principalQuotaResponse{}, &agentwire.AgentError{
			Code:    agentwire.CodeUnavailable,
			Message: fmt.Sprintf("stalwart admin API unreachable: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Never-used mailbox — return zeros, no error.
		return principalQuotaResponse{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return principalQuotaResponse{}, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart admin API returned %d", resp.StatusCode),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return principalQuotaResponse{}, fmt.Errorf("read response: %w", err)
	}
	var out principalQuotaResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return principalQuotaResponse{}, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart admin API returned unparseable body: %v", err),
		}
	}
	return out, nil
}

// requireEmail validates + lowercases an email-shaped string for agent
// commands. Returns an AgentError-typed *invalid_argument on failure so
// the panel sees a structured error code instead of "internal".
func requireEmail(raw string) (string, error) {
	if raw == "" {
		return "", &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "email parameter required"}
	}
	// Defence in depth: reject control characters + shell metachars
	// before the address hits a URL builder. Panel-side
	// internal/mailaddr.Canonicalise is the canonical validator; this
	// is just a last-mile guard against a malformed NDJSON input.
	if strings.ContainsAny(raw, " \t\n\r;&|<>`$\\(){}'\"!*?[]") {
		return "", &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "shell metacharacter in email"}
	}
	if !strings.Contains(raw, "@") {
		return "", &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "email missing '@'"}
	}
	return strings.ToLower(raw), nil
}

// okBody is the trivial positive response shape shared by the four
// cache-invalidate commands. Keeping it in a named struct so the
// cross-boundary contract test pins the wire shape rather than
// anonymous struct literals.
type okBody struct {
	Ok bool `json:"ok"`
}

// discardBody is used to feed an ignored but non-nil body to
// http.Request in tests; not used in production.
func discardBody() io.Reader { return bytes.NewReader(nil) }
