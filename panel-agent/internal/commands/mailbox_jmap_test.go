package commands

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxTestFixtures owns the process-global state these tests mutate
// (stalwartAdminURLFunc, stalwartAdminTokenFunc, stalwartHTTPClientFunc).
// Call t.Cleanup(f.restore) inside every test that calls f.wireJMAP.
type mailboxTestFixtures struct {
	tokenFilePath string
	origURLFunc   func() string
	origTokenFunc func() (string, error)
	origClient    func() *http.Client
}

// wireJMAP points the package-level lookups at a test HTTP server and
// a throwaway token file. Restore via t.Cleanup.
func wireJMAP(t *testing.T, server *httptest.Server) *mailboxTestFixtures {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "admin.token")
	if err := os.WriteFile(tokenPath, []byte("test-token-123\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	f := &mailboxTestFixtures{
		tokenFilePath: tokenPath,
		origURLFunc:   stalwartAdminURLFunc,
		origTokenFunc: stalwartAdminTokenFunc,
		origClient:    stalwartHTTPClientFunc,
	}
	stalwartAdminURLFunc = func() string { return server.URL }
	stalwartAdminTokenFunc = func() (string, error) { return "test-token-123", nil }
	stalwartHTTPClientFunc = func() *http.Client { return server.Client() }

	t.Cleanup(func() {
		stalwartAdminURLFunc = f.origURLFunc
		stalwartAdminTokenFunc = f.origTokenFunc
		stalwartHTTPClientFunc = f.origClient
	})
	return f
}

func TestInvalidateStalwartPrincipal_SuccessfulPaths(t *testing.T) {
	// Stalwart v0.16.0 can legitimately respond 200, 404, or 503 —
	// all three mean "there's nothing stale to purge". 404 happens
	// when the cache has never seen the principal (fresh mailbox);
	// 503 when Stalwart isn't running yet (first email-enable on a
	// domain); 200 is the common case. All three should succeed.
	for _, code := range []int{200, 204, 404, 503} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/api/principal/") {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token-123" {
					t.Errorf("missing or wrong bearer: %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(code)
			}))
			defer srv.Close()
			wireJMAP(t, srv)

			if err := invalidateStalwartPrincipal(context.Background(), "alice@example.com"); err != nil {
				t.Fatalf("expected success for %d, got: %v", code, err)
			}
		})
	}
}

func TestInvalidateStalwartPrincipal_FailurePaths(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		wantCode string
	}{
		{"unauthorized", 401, agentwire.CodeInternal},
		{"forbidden", 403, agentwire.CodeInternal},
		{"internal", 500, agentwire.CodeInternal},
		{"gateway", 502, agentwire.CodeInternal},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
			}))
			defer srv.Close()
			wireJMAP(t, srv)

			err := invalidateStalwartPrincipal(context.Background(), "alice@example.com")
			if err == nil {
				t.Fatalf("expected error for %d, got nil", tt.code)
			}
			var ae *agentwire.AgentError
			if !errors.As(err, &ae) {
				t.Fatalf("expected AgentError, got %T: %v", err, err)
			}
			if ae.Code != tt.wantCode {
				t.Errorf("err code: got %q, want %q", ae.Code, tt.wantCode)
			}
		})
	}
}

func TestInvalidateStalwartPrincipal_ConnectionError(t *testing.T) {
	// Close the server immediately so Do() returns a connection error,
	// not a status-code error. CodeUnavailable is the contract for
	// "Stalwart not reachable right now".
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	stalwartAdminURLFunc = func() string { return url }
	stalwartAdminTokenFunc = func() (string, error) { return "t", nil }
	stalwartHTTPClientFunc = func() *http.Client { return &http.Client{Timeout: 100 * time.Millisecond} }
	t.Cleanup(func() {
		stalwartAdminURLFunc = func() string { return defaultStalwartAdminURL }
		stalwartAdminTokenFunc = func() (string, error) {
			return "", os.ErrNotExist // default test default is no token
		}
		stalwartHTTPClientFunc = func() *http.Client { return &http.Client{Timeout: stalwartHTTPTimeout} }
	})

	err := invalidateStalwartPrincipal(context.Background(), "alice@example.com")
	if err == nil {
		t.Fatal("expected error from closed server, got nil")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != agentwire.CodeUnavailable {
		t.Errorf("err code: got %q, want %q", ae.Code, agentwire.CodeUnavailable)
	}
}

func TestGetStalwartPrincipalQuota_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method: got %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"usedBytes":15728640,"messageCount":42,"lastUsedAt":"2026-04-21T19:03:00Z"}`))
	}))
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := getStalwartPrincipalQuota(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.UsedBytes != 15728640 {
		t.Errorf("UsedBytes: got %d, want 15728640", got.UsedBytes)
	}
	if got.MessageCount != 42 {
		t.Errorf("MessageCount: got %d, want 42", got.MessageCount)
	}
	if got.LastUsedAt == nil {
		t.Fatal("LastUsedAt: got nil, want non-nil")
	}
}

func TestGetStalwartPrincipalQuota_NotFoundReturnsZeros(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := getStalwartPrincipalQuota(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("expected no error for 404, got: %v", err)
	}
	if got.UsedBytes != 0 || got.MessageCount != 0 || got.LastUsedAt != nil {
		t.Errorf("expected zero-value result for 404, got %+v", got)
	}
}

func TestGetStalwartPrincipalQuota_UnparseableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	wireJMAP(t, srv)

	_, err := getStalwartPrincipalQuota(context.Background(), "alice@example.com")
	if err == nil {
		t.Fatal("expected error for unparseable body, got nil")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != agentwire.CodeInternal {
		t.Errorf("err code: got %q, want %q", ae.Code, agentwire.CodeInternal)
	}
}

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
