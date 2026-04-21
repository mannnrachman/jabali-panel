package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// --- JMAP fake server ------------------------------------------------

// jmapHandler is the router signature for a fake JMAP server. Given a
// decoded method-args payload, return either a result (encoded into the
// response's method-call args) or an error that the fake serialises as
// a JMAP "error" method response.
type jmapHandler func(args json.RawMessage) (result any, jmapErr *jmapFakeError)

// jmapFakeError matches what jmapCall unmarshals: { type, description }.
type jmapFakeError struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// newJMAPServer builds a httptest.Server that speaks the JMAP wire
// contract expected by mailbox_jmap.go. Routes by method name; methods
// not in the map respond with a 400 so a broken expectation surfaces
// loudly instead of silently returning zeros.
func newJMAPServer(t *testing.T, routes map[string]jmapHandler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != jmapAPIPath {
			http.Error(w, "fake: wrong path "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "fake: wrong method "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		if u, p, ok := r.BasicAuth(); !ok || u != jmapAdminUser || p == "" {
			http.Error(w, "fake: missing basic auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req jmapRequestBody
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "fake: bad body "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.MethodCalls) != 1 {
			http.Error(w, "fake: expected exactly one methodCall", http.StatusBadRequest)
			return
		}
		call := req.MethodCalls[0]
		handler, ok := routes[call.Name]
		if !ok {
			http.Error(w, "fake: no route for method "+call.Name, http.StatusBadRequest)
			return
		}

		var rawArgs json.RawMessage
		switch v := call.Args.(type) {
		case json.RawMessage:
			rawArgs = v
		default:
			b, _ := json.Marshal(v)
			rawArgs = b
		}

		result, jmapErr := handler(rawArgs)
		resp := jmapResponseBody{
			MethodResponses: make([]jmapMethodCall, 1),
		}
		if jmapErr != nil {
			resp.MethodResponses[0] = jmapMethodCall{
				Name:   "error",
				Args:   jmapErr,
				CallID: call.CallID,
			}
		} else {
			resp.MethodResponses[0] = jmapMethodCall{
				Name:   call.Name,
				Args:   result,
				CallID: call.CallID,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// wireJMAP points the package-level URL + token + HTTP client funcs at
// a test httptest.Server and restores them in t.Cleanup. Tests that
// don't need a real server (pure-validation cases) can skip calling
// this altogether — the seams only fire when jmapCall is reached.
func wireJMAP(t *testing.T, server *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "admin.token")
	if err := os.WriteFile(tokenPath, []byte("test-token-123\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	origURL := stalwartAdminURLFunc
	origToken := stalwartAdminTokenFunc
	origClient := stalwartHTTPClientFunc

	stalwartAdminURLFunc = func() string { return server.URL }
	stalwartAdminTokenFunc = func() (string, error) { return "test-token-123", nil }
	stalwartHTTPClientFunc = func() *http.Client { return server.Client() }

	t.Cleanup(func() {
		stalwartAdminURLFunc = origURL
		stalwartAdminTokenFunc = origToken
		stalwartHTTPClientFunc = origClient
	})
}

// jmapHandlerReturning is a test helper: returns a jmapHandler that
// ignores its args and always emits the given result.
func jmapHandlerReturning(result any) jmapHandler {
	return func(_ json.RawMessage) (any, *jmapFakeError) {
		return result, nil
	}
}

// requireAgentErrorCode asserts err is an *AgentError with the given
// code. Shared across every _test.go in this package.
func requireAgentErrorCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", wantCode)
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != wantCode {
		t.Errorf("err code: got %q, want %q (msg: %s)", ae.Code, wantCode, ae.Message)
	}
}

// --- jmapCall direct tests -------------------------------------------

func TestJMAPCall_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	wireJMAP(t, srv)

	err := jmapCall(context.Background(), "Account/query", map[string]any{}, nil)
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != agentwire.CodeInternal {
		t.Errorf("code: got %q, want %q", ae.Code, agentwire.CodeInternal)
	}
	if !strings.Contains(ae.Message, "basic auth") {
		t.Errorf("message missing hint: %q", ae.Message)
	}
}

func TestJMAPCall_NotReachable_ReturnsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // kill the server but keep its URL pointed-at-a-dead-port

	origURL := stalwartAdminURLFunc
	origToken := stalwartAdminTokenFunc
	origClient := stalwartHTTPClientFunc
	stalwartAdminURLFunc = func() string { return url }
	stalwartAdminTokenFunc = func() (string, error) { return "t", nil }
	stalwartHTTPClientFunc = func() *http.Client { return &http.Client{Timeout: 100 * time.Millisecond} }
	t.Cleanup(func() {
		stalwartAdminURLFunc = origURL
		stalwartAdminTokenFunc = origToken
		stalwartHTTPClientFunc = origClient
	})

	err := jmapCall(context.Background(), "Account/query", map[string]any{}, nil)
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != agentwire.CodeUnavailable {
		t.Errorf("code: got %q, want %q", ae.Code, agentwire.CodeUnavailable)
	}
}

func TestJMAPCall_StalwartError_Propagates(t *testing.T) {
	// The fake emits a JMAP-level error method response. jmapCall
	// should translate this into an AgentError carrying the type +
	// description in its Message so the operator sees what went wrong.
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/query": func(_ json.RawMessage) (any, *jmapFakeError) {
			return nil, &jmapFakeError{Type: "invalidArguments", Description: "filter required"}
		},
	})
	defer srv.Close()
	wireJMAP(t, srv)

	err := jmapCall(context.Background(), "Account/query", map[string]any{}, nil)
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if !strings.Contains(ae.Message, "invalidArguments") || !strings.Contains(ae.Message, "filter required") {
		t.Errorf("message missing JMAP error parts: %q", ae.Message)
	}
}

// --- accountIDByEmail / accountQuota ---------------------------------

func TestAccountIDByEmail_Found(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/query": jmapHandlerReturning(jmapQueryResult{
			IDs: []string{"acct-123"}, Total: 1,
		}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	id, err := accountIDByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != "acct-123" {
		t.Errorf("id: got %q, want acct-123", id)
	}
}

func TestAccountIDByEmail_NotFound_ReturnsEmpty(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/query": jmapHandlerReturning(jmapQueryResult{IDs: nil, Total: 0}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	id, err := accountIDByEmail(context.Background(), "ghost@example.com")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if id != "" {
		t.Errorf("id: got %q, want empty for not-found", id)
	}
}

func TestAccountQuota_Happy(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/get": func(_ json.RawMessage) (any, *jmapFakeError) {
			return jmapGetResult{
				List: []json.RawMessage{
					json.RawMessage(`{"quotaUsed":15728640,"messageCount":42,"lastAuthenticatedAt":"2026-04-21T19:03:00Z"}`),
				},
			}, nil
		},
	})
	defer srv.Close()
	wireJMAP(t, srv)

	view, err := accountQuota(context.Background(), "acct-123")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if view.QuotaUsed != 15728640 || view.MessageCount != 42 {
		t.Errorf("view: %+v", view)
	}
	if view.LastAuthAt == nil {
		t.Fatal("lastAuthAt: got nil, want non-nil")
	}
}

func TestAccountQuota_RaceDestroyed_ReturnsZeros(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/get": jmapHandlerReturning(jmapGetResult{List: nil, NotFound: []string{"acct-gone"}}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	view, err := accountQuota(context.Background(), "acct-gone")
	if err != nil {
		t.Fatalf("empty list should not error (race with destroy): %v", err)
	}
	if view.QuotaUsed != 0 || view.MessageCount != 0 {
		t.Errorf("expected zeros, got %+v", view)
	}
}

// --- accountDestroy / domainDestroy ----------------------------------

func TestAccountDestroy_Happy(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/set": jmapHandlerReturning(jmapSetResult{Destroyed: []string{"acct-123"}}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	if err := accountDestroy(context.Background(), "acct-123"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestAccountDestroy_AlreadyGone_IsNotAnError(t *testing.T) {
	// Stalwart reports notDestroyed with type: "notFound" when the
	// id no longer exists. Our helper treats this as success.
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/set": func(_ json.RawMessage) (any, *jmapFakeError) {
			return jmapSetResult{
				NotDestroyed: map[string]json.RawMessage{
					"acct-gone": json.RawMessage(`{"type":"notFound"}`),
				},
			}, nil
		},
	})
	defer srv.Close()
	wireJMAP(t, srv)

	if err := accountDestroy(context.Background(), "acct-gone"); err != nil {
		t.Fatalf("notFound should not error: %v", err)
	}
}

func TestAccountDestroy_OtherReason_Errors(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Account/set": func(_ json.RawMessage) (any, *jmapFakeError) {
			return jmapSetResult{
				NotDestroyed: map[string]json.RawMessage{
					"acct-x": json.RawMessage(`{"type":"forbidden","description":"protected principal"}`),
				},
			}, nil
		},
	})
	defer srv.Close()
	wireJMAP(t, srv)

	err := accountDestroy(context.Background(), "acct-x")
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != agentwire.CodeInternal {
		t.Errorf("code: got %q", ae.Code)
	}
}

func TestDomainDestroy_Happy(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"Domain/set": jmapHandlerReturning(jmapSetResult{Destroyed: []string{"dom-42"}}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	if err := domainDestroy(context.Background(), "dom-42"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// --- requireEmail -----------------------------------------------------

func TestRequireEmail(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		isErr bool
	}{
		{"ok lower", "alice@example.com", "alice@example.com", false},
		{"ok mixed case lowered", "Alice@Example.COM", "alice@example.com", false},
		{"empty", "", "", true},
		{"no at", "alice.example.com", "", true},
		{"shell metachar semicolon", "a;@x.com", "", true},
		{"shell metachar newline", "a\n@x.com", "", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := requireEmail(tt.in)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
