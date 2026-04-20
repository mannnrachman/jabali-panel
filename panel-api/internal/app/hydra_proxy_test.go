package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterHydraProxy_ForwardsAllRoutes exercises each of the three
// registered paths (oauth2/*, /.well-known/*, /userinfo) and asserts
// the upstream sees the request verbatim with path UNCHANGED (Hydra
// relies on absolute paths — a stripped prefix would 404).
func TestRegisterHydraProxy_ForwardsAllRoutes(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := RegisterHydraProxy(r, upstream.URL); err != nil {
		t.Fatalf("RegisterHydraProxy: %v", err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()

	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		wantPath string
	}{
		{"oauth2_auth_get", "GET", "/oauth2/auth?client_id=abc", "", "/oauth2/auth"},
		{"oauth2_token_post", "POST", "/oauth2/token", "grant_type=authorization_code&code=x", "/oauth2/token"},
		{"oauth2_sessions_logout", "GET", "/oauth2/sessions/logout", "", "/oauth2/sessions/logout"},
		{"well_known_openid", "GET", "/.well-known/openid-configuration", "", "/.well-known/openid-configuration"},
		{"well_known_jwks", "GET", "/.well-known/jwks.json", "", "/.well-known/jwks.json"},
		{"userinfo_get", "GET", "/userinfo", "", "/userinfo"},
		{"userinfo_post", "POST", "/userinfo", "access_token=tok", "/userinfo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp *http.Response
			var err error
			switch tc.method {
			case "GET":
				resp, err = http.Get(srv.URL + tc.path)
			case "POST":
				resp, err = http.Post(srv.URL+tc.path, "application/x-www-form-urlencoded",
					strings.NewReader(tc.body))
			}
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("status=%d, want 200", resp.StatusCode)
			}
			if gotMethod != tc.method {
				t.Errorf("method=%q, want %q", gotMethod, tc.method)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path=%q, want %q (Hydra needs absolute paths — do NOT strip prefix)", gotPath, tc.wantPath)
			}
			if tc.body != "" && string(gotBody) != tc.body {
				t.Errorf("body=%q, want %q", gotBody, tc.body)
			}
		})
	}
}

// TestRegisterHydraProxy_DoesNotExposeAdmin asserts that /admin/* and
// /health/* remain unroutable through the proxy, since exposing them
// would leak client CRUD and token introspection to any caller. These
// must ONLY be reachable loopback via hydraclient, never through the
// panel's public listener.
func TestRegisterHydraProxy_DoesNotExposeAdmin(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := RegisterHydraProxy(r, upstream.URL); err != nil {
		t.Fatalf("RegisterHydraProxy: %v", err)
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Paths that MUST NOT forward to Hydra through the public proxy.
	blocked := []string{
		"/admin/clients",                 // client CRUD
		"/admin/oauth2/auth/requests/login",   // consent accept
		"/admin/oauth2/introspect",       // token introspection
		"/health/ready",                  // server health (not for clients)
	}
	for _, p := range blocked {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if upstreamHit {
			t.Fatalf("%s reached upstream — admin/health surface must NOT be proxied", p)
		}
		// Gin returns 404 for unrouted paths.
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s got 200 from panel — expected unrouted (admin paths must not be exposed)", p)
		}
	}
}

func TestRegisterHydraProxy_RejectsBadURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	err := RegisterHydraProxy(r, "http://\x00bad")
	if err == nil {
		t.Fatal("expected error for malformed upstream URL, got nil")
	}
}
