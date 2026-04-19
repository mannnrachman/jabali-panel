package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

func newCORSRouter(origins []string) *gin.Engine {
	r := gin.New()
	r.Use(middleware.CORS(origins))
	r.GET("/x", func(c *gin.Context) { c.String(200, "ok") })
	r.POST("/x", func(c *gin.Context) { c.String(200, "ok") })
	return r
}

func TestCORS_AllowedOriginIsReflected(t *testing.T) {
	t.Parallel()
	r := newCORSRouter([]string{"https://panel.example", "http://localhost:5173"})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://panel.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "https://panel.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, rec.Header().Get("Vary"), "Origin")
}

func TestCORS_ForbiddenOriginGetsNoAllowOrigin(t *testing.T) {
	t.Parallel()
	r := newCORSRouter([]string{"https://panel.example"})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code, "request still served (CORS is a browser check)")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"unlisted origins must not receive an Allow-Origin header")
}

func TestCORS_SameOriginRequestsSkipped(t *testing.T) {
	t.Parallel()
	r := newCORSRouter([]string{"https://panel.example"})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	// No Origin header → same-origin browser request; no CORS headers needed.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_PreflightReturns204(t *testing.T) {
	t.Parallel()
	r := newCORSRouter([]string{"https://panel.example"})

	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://panel.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://panel.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	assert.NotEmpty(t, rec.Header().Get("Access-Control-Max-Age"))
}

func TestCORS_SameOriginWithHeaderIsAutoAllowed(t *testing.T) {
	t.Parallel()
	// Firefox sends Origin even on same-origin fetch/XHR. Without reflecting
	// it back, the browser blocks the response (OpaqueResponseBlocking), which
	// manifests as a blank /login page after token expiry. Auto-allowed when
	// Origin's host:port == Request.Host.
	r := newCORSRouter(nil) // empty whitelist, like a default deploy

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "jabali-panel.local:8443"
	req.Header.Set("Origin", "https://jabali-panel.local:8443")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "https://jabali-panel.local:8443", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCORS_DifferentPortIsCrossOrigin(t *testing.T) {
	t.Parallel()
	// Different port = different origin. Must NOT auto-allow.
	r := newCORSRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "jabali-panel.local:8443"
	req.Header.Set("Origin", "https://jabali-panel.local:9000")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"different port must not be treated as same-origin")
}

func TestCORS_WildcardWithCredentialsRefused(t *testing.T) {
	t.Parallel()
	// We refuse to honour "*" because the refresh cookie requires credentials.
	// Configuring "*" is a misconfiguration; the middleware treats it the same
	// as "no allowed origins" and does not reflect the request.
	r := newCORSRouter([]string{"*"})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://anywhere.example")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		`"*" must never combine with credentials=true`)
}
