package hydraclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSanitizeMetadata_StripsTrustedKey is the direct unit test for
// Decision 7 at the purest level. Calling code paths that don't go
// through CreateClient still benefit from using sanitizeMetadata
// — guard it at the helper level so regression at ANY callsite
// surfaces immediately.
func TestSanitizeMetadata_StripsTrustedKey(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "trusted_true_stripped",
			in:   map[string]any{"trusted": true, "install_id": "abc"},
			want: map[string]any{"install_id": "abc"},
		},
		{
			name: "trusted_false_also_stripped",
			in:   map[string]any{"trusted": false, "install_id": "abc"},
			want: map[string]any{"install_id": "abc"},
		},
		{
			name: "trusted_string_stripped",
			in:   map[string]any{"trusted": "true", "install_id": "abc"},
			want: map[string]any{"install_id": "abc"},
		},
		{
			name: "other_keys_preserved",
			in:   map[string]any{"install_id": "abc", "owner": "shuki"},
			want: map[string]any{"install_id": "abc", "owner": "shuki"},
		},
		{
			name: "nil_input_returns_empty_map",
			in:   nil,
			want: map[string]any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeMetadata(tc.in)
			if got == nil {
				t.Fatalf("sanitizeMetadata returned nil — callers expect a non-nil map")
			}
			if _, ok := got["trusted"]; ok {
				t.Fatalf("trusted key survived strip: %+v", got)
			}
			if len(got) != len(tc.want) {
				t.Errorf("len=%d, want %d (got=%+v want=%+v)", len(got), len(tc.want), got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q: got %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

// TestCreateClient_StripsCallerSuppliedTrusted is the Decision 7 /
// ADR-0036 R1 end-to-end test: even if a caller hands in
// metadata.trusted=true, CreateClient MUST send metadata.trusted=false
// (or absent) to Hydra. The fake admin server captures the outgoing
// payload and asserts.
//
// Regression here = silent consent for any scope on any client. Do
// not delete or skip this test; a future "it's simpler to just
// let callers pass trusted" is exactly the change that re-opens the
// privilege escalation we're guarding against.
func TestCreateClient_StripsCallerSuppliedTrusted(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/clients" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"client_id":"cid-123","client_secret":"secret-abc"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	out, err := c.CreateClient(context.Background(), CreateClientInput{
		ClientName:              "Evil Client",
		RedirectURIs:            []string{"https://example.com/cb"},
		GrantTypes:              []string{"authorization_code"},
		ResponseTypes:           []string{"code"},
		Scope:                   "openid",
		TokenEndpointAuthMethod: "client_secret_post",
		// CRITICAL: attacker-supplied trusted=true. MUST NOT reach Hydra.
		Metadata: map[string]any{
			"trusted":    true,
			"install_id": "01HDMV...",
		},
	})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if out.ClientID != "cid-123" {
		t.Errorf("ClientID=%q, want cid-123", out.ClientID)
	}
	if out.ClientSecret.Raw() != "secret-abc" {
		t.Errorf("ClientSecret.Raw()=%q, want secret-abc", out.ClientSecret.Raw())
	}

	// The actual assertion: verify the payload that went over the
	// wire does NOT contain metadata.trusted.
	md, ok := capturedBody["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("request body had no metadata object: %+v", capturedBody)
	}
	if _, ok := md["trusted"]; ok {
		t.Fatalf("SECURITY: metadata.trusted=%v leaked to Hydra — Decision 7 regression. See client.go sanitizeMetadata.", md["trusted"])
	}
	// Non-trusted keys must pass through.
	if md["install_id"] != "01HDMV..." {
		t.Errorf("install_id didn't survive strip: %+v", md)
	}
	// Hydra's skip_consent is always false at create-time; the
	// trusted path goes through SetClientTrusted, never CreateClient.
	if got, ok := capturedBody["skip_consent"].(bool); !ok || got != false {
		t.Errorf("skip_consent=%v, want false at create time (Decision 7 defence in depth)", capturedBody["skip_consent"])
	}
}

// TestClientSecret_StringRedacts asserts the ClientSecret Stringer
// never leaks the raw bytes into log formatters. A regression here
// means the first slog line that captures the CreateClient result
// writes the secret to /var/log/*.
func TestClientSecret_StringRedacts(t *testing.T) {
	s := ClientSecret("super-secret-bytes")
	if s.String() != "[REDACTED]" {
		t.Errorf("String()=%q, want [REDACTED]", s.String())
	}
	if fmt.Sprintf("%s", s) != "[REDACTED]" {
		t.Errorf("fmt %%s =%q, want [REDACTED]", s)
	}
	if fmt.Sprintf("%v", s) != "[REDACTED]" {
		t.Errorf("fmt %%v =%q, want [REDACTED]", s)
	}
	// Raw() is the single escape hatch; must return the unredacted
	// bytes. Intentional to distinguish "I need the secret for seal"
	// from "I'm stringifying for a log line".
	if s.Raw() != "super-secret-bytes" {
		t.Errorf("Raw()=%q, want super-secret-bytes", s.Raw())
	}
}

// TestSetClientTrusted_MergesMetadata exercises the two-step GET+PUT
// flow. Hydra's PUT /admin/clients/{id} replaces the entire client
// record; our helper has to echo every field back or the install
// loses its redirect URIs / scopes.
func TestSetClientTrusted_MergesMetadata(t *testing.T) {
	var capturedPut map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"client_id":"cid-123",
				"client_name":"Test",
				"redirect_uris":["https://example.com/cb"],
				"grant_types":["authorization_code"],
				"response_types":["code"],
				"scope":"openid",
				"metadata":{"install_id":"abc"}
			}`)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &capturedPut)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{}`)
		}
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.SetClientTrusted(context.Background(), "cid-123", true); err != nil {
		t.Fatalf("SetClientTrusted: %v", err)
	}

	md, _ := capturedPut["metadata"].(map[string]any)
	if md["trusted"] != true {
		t.Errorf("metadata.trusted=%v, want true (server-side set)", md["trusted"])
	}
	if md["install_id"] != "abc" {
		t.Errorf("install_id merge lost — got %+v, want install_id preserved", md)
	}
	if capturedPut["skip_consent"] != true {
		t.Errorf("skip_consent=%v, want true (mirrors metadata.trusted)", capturedPut["skip_consent"])
	}
	// Echo-back: the RedirectURIs must survive or the install breaks.
	if uris, _ := capturedPut["redirect_uris"].([]any); len(uris) != 1 || uris[0] != "https://example.com/cb" {
		t.Errorf("redirect_uris not echoed — got %+v", capturedPut["redirect_uris"])
	}
}

// TestDeleteClient_IdempotentOn404 asserts a 404 maps to ErrNotFound
// — crucial for the apps framework's compensating transaction on
// install delete, which treats "already gone" as success.
func TestDeleteClient_IdempotentOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.DeleteClient(context.Background(), "cid-gone")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err=%v, want ErrNotFound", err)
	}
}

// TestCreateClient_MappingsOnHTTPErrors confirms the typed-error
// mapping. Admin-URL misconfigurations (401), client_id collisions
// (409), and generic 5xx each get a distinguishable error.
func TestCreateClient_MappingsOnHTTPErrors(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, "unauthorized"},
		{403, "unauthorized"}, // 403 → ErrUnauthorized too
		{409, "conflict"},
		{500, "http 500"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status=%d", tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"error":"demo"}`)
			}))
			defer srv.Close()
			c := New(srv.URL)
			_, err := c.CreateClient(context.Background(), CreateClientInput{
				ClientName: "x",
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("status=%d: err=%v, want substring %q", tc.status, err, tc.want)
			}
		})
	}
}
