package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// kratosProbe mounts RequireKratosSession with a probe handler that echoes
// the authenticated user's email + is_admin flag, so tests can assert the
// middleware populated the context correctly.
func kratosProbe(client *kratosclient.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/me", middleware.RequireKratosSession(client), func(c *gin.Context) {
		cl := ginctx.Claims(c)
		if cl == nil {
			c.String(http.StatusInternalServerError, "no claims")
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user_id":  cl.UserID,
			"email":    cl.Email,
			"is_admin": cl.IsAdmin,
		})
	})
	return r
}

// fakeKratos stands in for the Kratos public server in tests.
// status controls the HTTP code /sessions/whoami returns. body is the
// response JSON (for 200 cases).
func fakeKratos(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sessions/whoami") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
}

func TestRequireKratosSession_ValidCookie(t *testing.T) {
	t.Parallel()
	identityJSON := `{
		"id": "01KRATOS-ID",
		"traits": {"email": "user@example.com", "username": "alice", "is_admin": true}
	}`
	srv := fakeKratos(t, http.StatusOK, identityJSON)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid-session-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"user_id":"01KRATOS-ID"`)
	assert.Contains(t, rec.Body.String(), `"email":"user@example.com"`)
	assert.Contains(t, rec.Body.String(), `"is_admin":true`)
}

func TestRequireKratosSession_MissingCookie_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK, `{}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	// No cookie, no auth header either.
	rec := httptest.NewRecorder()

	kratosProbe(client).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_session")
}

// Critical security property: even if a legacy JWT is presented in the
// Authorization header, Kratos middleware must ignore it and reject the
// request as unauthenticated (no fallback to JWT validation).
// Closes adversarial review finding #1 from the M20 plan.
func TestRequireKratosSession_IgnoresBearerHeader(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK, `{}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	// No Kratos cookie, but a Bearer header is present — should be ignored.
	req.Header.Set("Authorization", "Bearer some.jwt.token")
	rec := httptest.NewRecorder()

	kratosProbe(client).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_session")
}

func TestRequireKratosSession_KratosReturns401_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusUnauthorized, `{"error":"no active session"}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "stale-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_session")
}

// Infrastructure failure must NOT masquerade as "unauthenticated" — that
// would force every user to re-login on every Kratos blip. Return 503 so
// the SPA can show a transient error and retry.
func TestRequireKratosSession_Kratos5xx_Returns503(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusInternalServerError, `{"error":"internal"}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid-looking-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "identity_service_unavailable")
	// Must NOT leak internal error details in the response body.
	assert.NotContains(t, rec.Body.String(), "internal")
}

// If Kratos is completely unreachable (network error), we should still
// return 503, not 401. Test by pointing the client at a closed server.
func TestRequireKratosSession_KratosUnreachable_Returns503(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK, `{}`)
	srv.Close() // close immediately — subsequent requests will fail

	client := kratosclient.NewClient(srv.URL, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "identity_service_unavailable")
}
