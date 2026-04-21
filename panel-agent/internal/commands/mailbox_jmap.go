package commands

// JMAP client for Stalwart v0.16's management API (ADR-0045).
//
// Stalwart's v0.15 REST surface under /api/principal/... is gone; all
// management operations happen through the JMAP-shaped endpoint at /jmap.
// This file is the narrow client the panel-agent command handlers use:
//
//  - accountIDByEmail(ctx, email): resolve a registry Account id from an
//    email string (uses Account/query with a filter).
//  - accountQuota(ctx, id): read quotaUsed + messageCount + lastAuthenticatedAt
//    for an Account (uses Account/get).
//  - accountDestroy(ctx, id): drop a stale registry Account record (uses
//    Account/set { destroy }).
//  - domainIDByName + domainDestroy: same shape for Domain objects.
//
// Auth is HTTP Basic with ("admin", <token from stalwart-admin.token>),
// paired with STALWART_RECOVERY_ADMIN=admin:<token> in stalwart.env so
// Stalwart accepts it against the env-seeded admin credential. A follow-up
// install.sh commit wires that env line.
//
// Timeouts: loopback JMAP calls resolve in single-digit ms on a healthy
// host; a 5-second cap catches process-wedge without surprising operators.

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

const (
	defaultStalwartAdminURL       = "http://127.0.0.1:8446"
	defaultStalwartAdminTokenPath = "/etc/jabali-panel/stalwart-admin.token"

	// jmapAPIPath is the Stalwart default path for the JMAP method endpoint.
	// Normally the client would discover this via GET /jmap/session; we
	// hardcode for simplicity because the panel-agent only talks to a
	// Stalwart we provisioned ourselves.
	jmapAPIPath = "/jmap"

	// jmapSessionPath is the session-discovery endpoint — not currently
	// used but reserved for a future capability-aware reconciler.
	jmapSessionPath = "/jmap/session"

	envStalwartAdminURL       = "JABALI_STALWART_ADMIN_URL"
	envStalwartAdminTokenPath = "JABALI_STALWART_ADMIN_TOKEN_PATH"

	stalwartHTTPTimeout = 5 * time.Second

	// jmapAdminUser is the fixed username the panel-agent uses when
	// authenticating against Stalwart. Paired with the token from
	// stalwart-admin.token via STALWART_RECOVERY_ADMIN.
	jmapAdminUser = "admin"
)

// USING is the JMAP capability list the client advertises in every
// request body. Matches what stalwart-cli itself sends (see upstream
// cli/src/jmap/protocol.rs USING constant).
var jmapUsing = []string{
	"urn:ietf:params:jmap:core",
	"urn:stalwart:jmap",
}

// Test injection seams. _test.go files swap these and restore in Cleanup.
var stalwartHTTPClientFunc = func() *http.Client {
	return &http.Client{Timeout: stalwartHTTPTimeout}
}

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

// jmapRequestBody is the wire shape for a JMAP request (RFC 8620 §3.3).
// Matches the stalwart-cli Rust struct exactly.
type jmapRequestBody struct {
	Using       []string          `json:"using"`
	MethodCalls []jmapMethodCall  `json:"methodCalls"`
	CreatedIds  map[string]string `json:"createdIds,omitempty"`
}

// jmapMethodCall is a [name, args, callId] triple. We marshal as a
// 3-element JSON array to match the JMAP wire contract.
type jmapMethodCall struct {
	Name   string
	Args   any
	CallID string
}

func (c jmapMethodCall) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{c.Name, c.Args, c.CallID})
}

func (c *jmapMethodCall) UnmarshalJSON(data []byte) error {
	var arr [3]json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("method call must be a 3-element array: %w", err)
	}
	if err := json.Unmarshal(arr[0], &c.Name); err != nil {
		return fmt.Errorf("method name: %w", err)
	}
	c.Args = arr[1] // keep as RawMessage; caller decodes into their struct
	if err := json.Unmarshal(arr[2], &c.CallID); err != nil {
		return fmt.Errorf("method callId: %w", err)
	}
	return nil
}

// jmapResponseBody — response shape. Mirror of jmapRequestBody with
// `methodResponses` instead of `methodCalls`.
type jmapResponseBody struct {
	MethodResponses []jmapMethodCall  `json:"methodResponses"`
	SessionState    string            `json:"sessionState,omitempty"`
	CreatedIds      map[string]string `json:"createdIds,omitempty"`
}

// jmapCall issues a single-method JMAP POST against /jmap and decodes
// the single response into `out`. If Stalwart returns a "error" method
// (wrong credentials, invalid args, unknown method), this returns a
// structured AgentError. Connection-level failure is CodeUnavailable.
func jmapCall(ctx context.Context, method string, args any, out any) error {
	body := jmapRequestBody{
		Using: jmapUsing,
		MethodCalls: []jmapMethodCall{
			{Name: method, Args: args, CallID: "c0"},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal jmap request: %w", err)
	}

	url := stalwartAdminURLFunc() + jmapAPIPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build jmap request: %w", err)
	}
	token, err := stalwartAdminTokenFunc()
	if err != nil {
		return fmt.Errorf("admin token: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(jmapAdminUser, token)

	resp, err := stalwartHTTPClientFunc().Do(req)
	if err != nil {
		return &agentwire.AgentError{
			Code:    agentwire.CodeUnavailable,
			Message: fmt.Sprintf("stalwart JMAP unreachable: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "stalwart JMAP rejected basic auth — admin token rotated without agent restart?",
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart JMAP returned HTTP %d", resp.StatusCode),
		}
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read jmap response: %w", err)
	}
	var parsed jmapResponseBody
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart JMAP returned unparseable body: %v", err),
		}
	}
	if len(parsed.MethodResponses) != 1 {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("expected 1 method response, got %d", len(parsed.MethodResponses)),
		}
	}

	mr := parsed.MethodResponses[0]
	if mr.Name == "error" {
		// JMAP-level error: args carry { type, description }.
		var errResp struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		}
		if raw, ok := mr.Args.(json.RawMessage); ok {
			_ = json.Unmarshal(raw, &errResp)
		}
		return &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart JMAP error: %s — %s",
				errResp.Type, errResp.Description),
		}
	}
	if mr.Name != method {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart JMAP method mismatch: expected %q, got %q", method, mr.Name),
		}
	}

	if out == nil {
		return nil
	}
	raw, ok := mr.Args.(json.RawMessage)
	if !ok {
		return fmt.Errorf("internal: method-call args not RawMessage")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart JMAP response decode: %v", err),
		}
	}
	return nil
}

// jmapQueryResult is the shape of <Object>/query responses.
type jmapQueryResult struct {
	IDs   []string `json:"ids"`
	Total uint64   `json:"total"`
}

// jmapGetResult wraps the `list` array returned by <Object>/get. Each
// element is an opaque raw message — callers decode into their own
// struct with the fields they care about.
type jmapGetResult struct {
	List     []json.RawMessage `json:"list"`
	NotFound []string          `json:"notFound"`
}

// jmapSetResult is the shape of <Object>/set responses.
type jmapSetResult struct {
	Created   map[string]json.RawMessage `json:"created,omitempty"`
	Updated   map[string]json.RawMessage `json:"updated,omitempty"`
	Destroyed []string                   `json:"destroyed,omitempty"`
	// NotCreated/NotUpdated/NotDestroyed carry { type, description } per id.
	NotCreated   map[string]json.RawMessage `json:"notCreated,omitempty"`
	NotUpdated   map[string]json.RawMessage `json:"notUpdated,omitempty"`
	NotDestroyed map[string]json.RawMessage `json:"notDestroyed,omitempty"`
}

// accountIDByEmail resolves a registry Account's id by its email
// address. Returns "" + nil if Stalwart doesn't have a record yet
// (mailbox in panel DB but nobody has authenticated yet — registry is
// populated lazily on first auth per ADR-0045). The caller decides
// whether absence is an error for their operation.
//
// Filter property: `emailAddress` (schema-verified — x:UserAccount has
// emailAddress as format:"emailAddress", derived server-side from
// name+'@'+domain.name; Group accounts use the same property name).
func accountIDByEmail(ctx context.Context, email string) (string, error) {
	args := map[string]any{
		"filter": map[string]any{
			"property": "emailAddress",
			"value":    email,
		},
		"limit": 1,
	}
	var result jmapQueryResult
	if err := jmapCall(ctx, "x:Account/query", args, &result); err != nil {
		return "", err
	}
	if len(result.IDs) == 0 {
		return "", nil
	}
	return result.IDs[0], nil
}

// accountQuotaView is the subset of the x:UserAccount JMAP object
// that mailbox.usage needs. Property name + type verified against
// the upstream schema at
// github.com/stalwartlabs/stalwart/resources/schema/schema.json.gz
// (pulled 2026-04-21, SHA-URL-b64 aJJKvnpsjjwAEzJ0eKDWdfSLK_RvBKsLhk8BwCq-7qA).
//
// SCHEMA GAP — v0.16 does not expose per-Account properties
// equivalent to v0.15's usageResponse.messageCount or lastUsedAt.
// Verified absent from schema.json (regex grep:
//
//	'messageCount'        → 0 occurrences
//	'lastAuth*|lastLogin*' → 0 occurrences
//	'lastUsed*'           → 0 occurrences
//
// mailbox.usage therefore always returns message_count=0 and
// last_used_at="" against v0.16. The wire contract is preserved
// (panel-side sampler still sets last_usage_bytes); the two empty
// fields are pinned to their zero-value semantics in the handler.
// A future Stalwart version exposing message count or last-auth
// timestamp lifts this gap with a one-line property addition.
type accountQuotaView struct {
	UsedDiskQuota uint64 `json:"usedDiskQuota"`
}

// accountQuota fetches usage bytes for a resolved account id.
func accountQuota(ctx context.Context, id string) (accountQuotaView, error) {
	args := map[string]any{
		"ids":        []string{id},
		"properties": []string{"usedDiskQuota"},
	}
	var result jmapGetResult
	if err := jmapCall(ctx, "x:Account/get", args, &result); err != nil {
		return accountQuotaView{}, err
	}
	if len(result.List) == 0 {
		// Account existed at query time but vanished at get time —
		// race with a parallel delete. Treat as zero usage, no error.
		return accountQuotaView{}, nil
	}
	var view accountQuotaView
	if err := json.Unmarshal(result.List[0], &view); err != nil {
		return accountQuotaView{}, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("decode x:Account/get result: %v", err),
		}
	}
	return view, nil
}

// accountDestroy removes a registry Account record. Idempotent: if
// the id doesn't exist (already-destroyed race), the response's
// notDestroyed entry is ignored rather than surfaced as an error.
func accountDestroy(ctx context.Context, id string) error {
	args := map[string]any{
		"destroy": []string{id},
	}
	var result jmapSetResult
	if err := jmapCall(ctx, "x:Account/set", args, &result); err != nil {
		return err
	}
	// Success if id appears in destroyed, or in notDestroyed with a
	// "not found" reason (already gone). Any other notDestroyed reason
	// is an error.
	for _, d := range result.Destroyed {
		if d == id {
			return nil
		}
	}
	if reason, ok := result.NotDestroyed[id]; ok {
		var r struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(reason, &r)
		if r.Type == "notFound" {
			return nil
		}
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart Account/set destroy %s refused: %s", id, string(reason)),
		}
	}
	// No signal either way — return ok (JMAP's contract is that the id
	// must appear in either destroyed or notDestroyed, so this branch
	// shouldn't fire, but don't fail the caller on a malformed response).
	return nil
}

// domainIDByName resolves a registry Domain's id by its name.
// Semantics identical to accountIDByEmail.
func domainIDByName(ctx context.Context, name string) (string, error) {
	args := map[string]any{
		"filter": map[string]any{
			"property": "name",
			"value":    name,
		},
		"limit": 1,
	}
	var result jmapQueryResult
	if err := jmapCall(ctx, "x:Domain/query", args, &result); err != nil {
		return "", err
	}
	if len(result.IDs) == 0 {
		return "", nil
	}
	return result.IDs[0], nil
}

// domainDestroy removes a registry Domain record. Same
// not-found-is-ok semantics as accountDestroy.
func domainDestroy(ctx context.Context, id string) error {
	args := map[string]any{
		"destroy": []string{id},
	}
	var result jmapSetResult
	if err := jmapCall(ctx, "x:Domain/set", args, &result); err != nil {
		return err
	}
	for _, d := range result.Destroyed {
		if d == id {
			return nil
		}
	}
	if reason, ok := result.NotDestroyed[id]; ok {
		var r struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(reason, &r)
		if r.Type == "notFound" {
			return nil
		}
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("stalwart Domain/set destroy %s refused: %s", id, string(reason)),
		}
	}
	return nil
}

// requireEmail validates + lowercases an email-shaped string for agent
// commands. Unchanged from the v0.15 implementation — the email
// validation doesn't depend on the transport.
func requireEmail(raw string) (string, error) {
	if raw == "" {
		return "", &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "email parameter required"}
	}
	if strings.ContainsAny(raw, " \t\n\r;&|<>`$\\(){}'\"!*?[]") {
		return "", &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "shell metacharacter in email"}
	}
	if !strings.Contains(raw, "@") {
		return "", &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "email missing '@'"}
	}
	return strings.ToLower(raw), nil
}

// okBody is the trivial positive response shape shared by commands
// whose ack carries no payload.
type okBody struct {
	Ok bool `json:"ok"`
}
