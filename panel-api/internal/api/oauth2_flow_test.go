package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/hydraclient"

	"github.com/gin-gonic/gin"
)

// setupHydraFake returns (fake admin server, recorded requests) where
// the recorded requests capture method+path+body for each admin-API
// call the handler makes. The server answers with the configured
// canned responses keyed by path prefix so a single fake covers all
// hydraclient methods the handlers call.
type hydraFake struct {
	srv    *httptest.Server
	routes map[string]hydraFakeResponse
	calls  []hydraFakeCall
}

type hydraFakeResponse struct {
	Status int
	Body   string
}

type hydraFakeCall struct {
	Method string
	Path   string
	Body   map[string]any
}

func newHydraFake(t *testing.T, routes map[string]hydraFakeResponse) *hydraFake {
	t.Helper()
	f := &hydraFake{routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &body)
		}
		f.calls = append(f.calls, hydraFakeCall{Method: r.Method, Path: r.URL.Path, Body: body})
		// Match by longest-prefix path match so /admin/oauth2/auth/requests/consent/accept
		// hits a different handler than /admin/oauth2/auth/requests/consent.
		var match hydraFakeResponse
		var matchLen int
		for prefix, resp := range f.routes {
			if strings.HasPrefix(r.URL.Path, prefix) && len(prefix) > matchLen {
				match = resp
				matchLen = len(prefix)
			}
		}
		if matchLen == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(match.Status)
		_, _ = w.Write([]byte(match.Body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *hydraFake) client() *hydraclient.Client {
	return hydraclient.New(f.srv.URL)
}

// newTestEngine wires a gin.Engine with a RequireKratosSession-
// equivalent middleware that injects fixed claims + an
// ory_kratos_session cookie into every request. Lets us exercise the
// OAuth2 flow routes without a real Kratos upstream.
func newTestEngine(t *testing.T, cfg OAuth2FlowHandlerConfig, kratosCookie string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	authMiddleware := func(c *gin.Context) {
		// Simulate RequireKratosSession's effect: populate claims +
		// ensure the cookie is seen by downstream handlers.
		ginctx.SetClaims(c, &auth.AccessClaims{
			UserID:  "user-ulid-abc",
			Email:   "user@example.com",
			IsAdmin: false,
		})
		if kratosCookie != "" {
			// Inject the cookie into the request so computeIDPSessionID sees it.
			c.Request.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: kratosCookie})
		}
		c.Next()
	}
	protected := r.Group("/api/v1", authMiddleware)

	RegisterOAuth2FlowRoutes(protected, r, cfg)
	return r
}

// TestLoginStart_PassesIdentityProviderSessionID is the Decision 5
// end-to-end test on the handler: GET /oauth2-login → body sent to
// Hydra's /admin/oauth2/auth/requests/login/accept has
// identity_provider_session_id = SHA-256(Kratos cookie). Without
// this, the Kratos revoke → Hydra revoke cascade breaks.
func TestLoginStart_PassesIdentityProviderSessionID(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/login/accept": {
			Status: 200,
			Body:   `{"redirect_to":"https://panel/continue"}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{
		Hydra: fake.client(),
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := newTestEngine(t, cfg, "raw-kratos-cookie")

	req := httptest.NewRequest(http.MethodGet, "/oauth2-login?login_challenge=chal-abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d, want 302 (body=%s)", w.Code, w.Body.String())
	}

	// Verify the accept call body carries the Decision 5 field with the
	// expected hash.
	wantHashBytes := sha256.Sum256([]byte("raw-kratos-cookie"))
	wantHash := hex.EncodeToString(wantHashBytes[:])

	var acceptCall *hydraFakeCall
	for i := range fake.calls {
		if strings.HasPrefix(fake.calls[i].Path, "/admin/oauth2/auth/requests/login/accept") {
			acceptCall = &fake.calls[i]
			break
		}
	}
	if acceptCall == nil {
		t.Fatal("no accept call captured")
	}
	got, _ := acceptCall.Body["identity_provider_session_id"].(string)
	if got == "" {
		t.Fatal("SECURITY: identity_provider_session_id empty on login-accept. Decision 5 regression — Kratos revoke will not cascade to Hydra tokens.")
	}
	if got != wantHash {
		t.Errorf("identity_provider_session_id=%q, want %q (must be SHA-256 of raw cookie for the revocation cascade)", got, wantHash)
	}
	if subj, _ := acceptCall.Body["subject"].(string); subj != "user-ulid-abc" {
		t.Errorf("subject=%q, want user-ulid-abc", subj)
	}
}

// TestLoginStart_RefusesWithoutSessionCookie is the belt-and-
// suspenders half of Decision 5: even if middleware somehow failed to
// set the cookie, the handler must NOT call AcceptLoginRequest with
// an empty IdP session id. The cascade silently breaks if we do; the
// 401 response surfaces the regression.
func TestLoginStart_RefusesWithoutSessionCookie(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{})
	cfg := OAuth2FlowHandlerConfig{
		Hydra: fake.client(),
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	// Pass empty cookie → computeIDPSessionID returns "".
	r := newTestEngine(t, cfg, "")

	req := httptest.NewRequest(http.MethodGet, "/oauth2-login?login_challenge=chal-abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401 (missing cookie must refuse)", w.Code)
	}
	// And no accept call should have been made.
	for _, c := range fake.calls {
		if strings.Contains(c.Path, "accept") {
			t.Fatal("SECURITY: Hydra accept was called despite missing Kratos cookie — Decision 5 regression")
		}
	}
}

// TestConsentStart_TrustedAutoAccepts tests the happy path for
// panel-managed OIDC clients: metadata.trusted=true → auto-accept +
// 302 to the redirect_to URL Hydra returned. No SPA redirect.
func TestConsentStart_TrustedAutoAccepts(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent/accept": {
			Status: 200,
			Body:   `{"redirect_to":"https://oidc-client/callback"}`,
		},
		// Prefix match: "/admin/oauth2/auth/requests/consent" is shorter than
		// the "accept" path, so GET below hits here.
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"consent-chal",
				"subject":"user-ulid-abc",
				"client":{
					"client_id":"cid",
					"client_name":"Trusted App",
					"metadata":{"trusted":true},
					"scope":"openid"
				},
				"requested_scope":["openid"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	req := httptest.NewRequest(http.MethodGet, "/oauth2-consent?consent_challenge=consent-chal", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d, want 302 (body=%s)", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://oidc-client/callback" {
		t.Errorf("Location=%q, want https://oidc-client/callback (Hydra's redirect_to must pass through)", loc)
	}
	// Confirm an auto-accept POST was made (not a browser redirect to SPA).
	var sawAccept bool
	for _, c := range fake.calls {
		if strings.Contains(c.Path, "consent/accept") {
			sawAccept = true
		}
	}
	if !sawAccept {
		t.Fatal("expected consent/accept to be called on trusted path")
	}
}

// TestConsentStart_UntrustedNeverAutoAccepts is the Decision 7 test.
// Even if the request looks legitimate, an untrusted client must
// route through the SPA consent UI. Auto-accept on untrusted is
// silent consent for any scope — the worst-case regression of M16.
func TestConsentStart_UntrustedNeverAutoAccepts(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"consent-chal",
				"subject":"user-ulid-abc",
				"client":{
					"client_id":"cid",
					"client_name":"Random Third-Party App",
					"metadata":{"trusted":false,"owner":"third-party"},
					"scope":"openid profile"
				},
				"requested_scope":["openid","profile"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	req := httptest.NewRequest(http.MethodGet, "/oauth2-consent?consent_challenge=consent-chal", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status=%d, want 302 to SPA consent UI (body=%s)", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/consent?challenge=consent-chal") {
		t.Errorf("Location=%q, want /consent?challenge=consent-chal (untrusted must redirect to SPA, not auto-accept)", loc)
	}
	// Hardest invariant: NO accept call whatsoever on the untrusted path.
	for _, c := range fake.calls {
		if strings.Contains(c.Path, "consent/accept") {
			t.Fatalf("SECURITY: consent/accept called for untrusted client — Decision 7 regression. call=%+v", c)
		}
	}
}

// TestConsentStart_MalformedTrustedMetadataFailsClosed tests the
// scenario where an attacker (somehow) sets metadata.trusted to a
// non-bool value. Client2.Trusted() returns false for non-bool, so
// the handler routes through the SPA UI, not auto-accept.
func TestConsentStart_MalformedTrustedMetadataFailsClosed(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"c",
				"subject":"user-ulid-abc",
				"client":{
					"client_id":"cid",
					"client_name":"x",
					"metadata":{"trusted":"true"},
					"scope":"openid"
				},
				"requested_scope":["openid"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	req := httptest.NewRequest(http.MethodGet, "/oauth2-consent?consent_challenge=c", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/consent?challenge=") {
		t.Errorf("malformed trusted metadata must route to SPA UI, not auto-accept; got Location=%q", loc)
	}
}

// TestConsentAccept_SubjectMismatchRefuses asserts the cross-user
// consent guard: a challenge whose subject != the Kratos session
// user is 403'd. Defense against a SPA bug or malicious JS that
// POSTs a stolen challenge.
func TestConsentAccept_SubjectMismatchRefuses(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"c",
				"subject":"DIFFERENT-user",
				"client":{"client_id":"cid","client_name":"x","metadata":{},"scope":"openid"},
				"requested_scope":["openid"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	body := `{"challenge":"c","grant_scope":["openid"]}`
	req := httptest.NewRequest(http.MethodPost, "/oauth2-consent/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403 (subject mismatch)", w.Code)
	}
	for _, c := range fake.calls {
		if strings.Contains(c.Path, "consent/accept") {
			t.Fatal("SECURITY: accept called despite subject mismatch")
		}
	}
}

// TestConsentAccept_GrantScopeSubsetEnforced asserts a SPA can't
// grant scopes the user wasn't asked for. Hydra rejects this, but
// double-checking avoids the round-trip and gives a cleaner error.
func TestConsentAccept_GrantScopeSubsetEnforced(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"c",
				"subject":"user-ulid-abc",
				"client":{"client_id":"cid","client_name":"x","metadata":{},"scope":"openid"},
				"requested_scope":["openid"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	// grant scope includes "admin" which wasn't requested.
	body := `{"challenge":"c","grant_scope":["openid","admin"]}`
	req := httptest.NewRequest(http.MethodPost, "/oauth2-consent/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (grant_scope not subset)", w.Code)
	}
}

// TestConsentMetadata_UnknownScopeRendersObservable asserts the
// fail-loud behavior for unknown scopes: the SPA still gets an entry
// it can render, but with copy that makes the reviewer notice.
func TestConsentMetadata_UnknownScopeRendersObservable(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"c",
				"subject":"user-ulid-abc",
				"client":{"client_id":"cid","client_name":"x","metadata":{},"scope":"openid mystery_scope"},
				"requested_scope":["openid","mystery_scope"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/oauth2/consent/c", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var md ConsentMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &md); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(md.RequestedScope) != 2 {
		t.Fatalf("len=%d, want 2", len(md.RequestedScope))
	}
	var mystery *ScopeWithLabel
	for i := range md.RequestedScope {
		if md.RequestedScope[i].Scope == "mystery_scope" {
			mystery = &md.RequestedScope[i]
		}
	}
	if mystery == nil {
		t.Fatal("mystery_scope not rendered — unknown scope silently dropped is a regression")
	}
	if !strings.Contains(mystery.Short, "Unknown") {
		t.Errorf("Short label for unknown scope = %q, want Unknown-prefixed copy so reviewer notices", mystery.Short)
	}
}

// TestLoginStart_CallerSuppliedTrustedHintIgnored confirms that
// query params / headers / body can't affect the trust decision.
// The consent handler ONLY reads client.metadata.trusted from
// Hydra's consent request record. This test fires GET with a
// ?trusted=true query param and verifies it has no effect.
func TestConsentStart_CallerSuppliedTrustedHintIgnored(t *testing.T) {
	fake := newHydraFake(t, map[string]hydraFakeResponse{
		"/admin/oauth2/auth/requests/consent": {
			Status: 200,
			Body: `{
				"challenge":"c",
				"subject":"user-ulid-abc",
				"client":{
					"client_id":"cid",
					"client_name":"x",
					"metadata":{"trusted":false},
					"scope":"openid"
				},
				"requested_scope":["openid"]
			}`,
		},
	})
	cfg := OAuth2FlowHandlerConfig{Hydra: fake.client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := newTestEngine(t, cfg, "raw-cookie")

	// Attacker-controlled query param.
	req := httptest.NewRequest(http.MethodGet, "/oauth2-consent?consent_challenge=c&trusted=true&skip_consent=true", nil)
	req.Header.Set("X-Trusted", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/consent?challenge=") {
		t.Errorf("attacker-supplied trusted hint respected — Location=%q, want SPA redirect", loc)
	}
	for _, call := range fake.calls {
		if strings.Contains(call.Path, "consent/accept") {
			t.Fatalf("SECURITY: accept called due to attacker hint — Decision 7 regression. call=%+v", call)
		}
	}
}

// reportErrorBody is a debug helper when a test fails and the
// response body would help diagnose. Unused in green runs.
var _ = fmt.Sprintf
