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

// TestAcceptLoginRequest_PassesIdentityProviderSessionID is the
// Decision 5 invariant test: every login-accept MUST include
// identity_provider_session_id so Hydra's session-revocation cascade
// can invalidate tokens when the Kratos session is revoked.
//
// The handler (oauth2_flow.go) is responsible for computing the
// SHA-256 of the Kratos session cookie and passing it via
// AcceptLoginInput.IdentityProviderSessionID; this test covers the
// hydraclient half of the contract — the HTTP body actually carries
// the field, not-optional, no silent-drop.
//
// If this test goes red because someone removed
// identity_provider_session_id from the body: do not silence. Without
// it, a logged-out user can replay tokens for their access/refresh
// TTL (30m/30d).
func TestAcceptLoginRequest_PassesIdentityProviderSessionID(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/admin/oauth2/auth/requests/login/accept") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"redirect_to":"https://panel/oauth2/auth?continue=1"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	redirect, err := c.AcceptLoginRequest(context.Background(), "challenge-abc", AcceptLoginInput{
		Subject:                   "user-ulid-123",
		IdentityProviderSessionID: "sha256-of-kratos-cookie",
		Remember:                  true,
		RememberFor:               0,
	})
	if err != nil {
		t.Fatalf("AcceptLoginRequest: %v", err)
	}
	if !strings.Contains(redirect, "continue=1") {
		t.Errorf("redirect=%q, want upstream's redirect_to to pass through", redirect)
	}

	if got, ok := capturedBody["identity_provider_session_id"].(string); !ok || got == "" {
		t.Fatalf("SECURITY: identity_provider_session_id missing/empty in accept body: %+v\n"+
			"Decision 5 regression — Kratos session revoke will NOT cascade to Hydra tokens.",
			capturedBody)
	} else if got != "sha256-of-kratos-cookie" {
		t.Errorf("identity_provider_session_id=%q, want sha256-of-kratos-cookie (caller value must pass through)", got)
	}
	if got, _ := capturedBody["subject"].(string); got != "user-ulid-123" {
		t.Errorf("subject=%q, want user-ulid-123", got)
	}
}

// TestAcceptLoginRequest_RejectsEmptyIdentityProviderSessionID is the
// belt-and-suspenders to Decision 5. Even if a caller forgets to
// populate the field, the helper MUST NOT silently send an empty
// string (Hydra accepts empty; the cascade silently breaks). The
// handler's job is to refuse; the client's job is to make empty
// observable. We assert observability here — the field reaches the
// wire as "" so handler unit tests can catch it upstream.
//
// We deliberately DON'T add a nil-guard in the client. The handler
// is the single entry point; one check in one place is easier to
// reason about than two diverging guards.
func TestAcceptLoginRequest_EmptyIDPSessionIDReachesWire(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"redirect_to":"x"}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, _ = c.AcceptLoginRequest(context.Background(), "chal", AcceptLoginInput{
		Subject: "u",
		// IdentityProviderSessionID deliberately empty.
	})
	got, ok := capturedBody["identity_provider_session_id"]
	if !ok {
		t.Fatalf("identity_provider_session_id key missing — handler tests can't observe it")
	}
	if got != "" {
		t.Fatalf("expected empty string on wire for empty input, got %v", got)
	}
}

// TestClient2_Trusted covers the Trusted() metadata extraction used
// by the consent handler to gate auto-accept. Three cases: true, false,
// and "malformed" — the last must fail closed (no auto-accept on
// parse error).
func TestClient2_Trusted(t *testing.T) {
	cases := []struct {
		name string
		md   map[string]any
		want bool
	}{
		{"true", map[string]any{"trusted": true}, true},
		{"false", map[string]any{"trusted": false}, false},
		{"missing", map[string]any{}, false},
		{"nil_metadata", nil, false},
		{"wrong_type_string", map[string]any{"trusted": "true"}, false}, // fail closed
		{"wrong_type_number", map[string]any{"trusted": 1}, false},      // fail closed
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Client2{Metadata: tc.md}
			if got := c.Trusted(); got != tc.want {
				t.Errorf("Trusted()=%v, want %v (metadata=%+v)", got, tc.want, tc.md)
			}
		})
	}
}

// TestGetConsentRequest_ParsesTrustedFlag covers the wire-level
// behavior: a consent_request with metadata.trusted=true on the
// client must deserialize into a Client2 whose Trusted() returns
// true. Guards against a regression where Client2 loses its Metadata
// field or the JSON tag.
func TestGetConsentRequest_ParsesTrustedFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"challenge":"consent-chal",
			"subject":"u",
			"client":{
				"client_id":"c1",
				"client_name":"My App",
				"metadata":{"trusted":true,"install_id":"abc"},
				"scope":"openid email"
			},
			"requested_scope":["openid","email"]
		}`)
	}))
	defer srv.Close()

	c := New(srv.URL)
	req, err := c.GetConsentRequest(context.Background(), "consent-chal")
	if err != nil {
		t.Fatalf("GetConsentRequest: %v", err)
	}
	if !req.Client.Trusted() {
		t.Errorf("Trusted()=false, want true — metadata parsing regression")
	}
	if len(req.RequestedScope) != 2 {
		t.Errorf("RequestedScope=%v, want [openid email]", req.RequestedScope)
	}
}
