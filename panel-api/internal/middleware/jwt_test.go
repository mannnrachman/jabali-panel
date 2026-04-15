package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

const testJWTSecret = "middleware-test-secret-32-bytes-min"

func newIssuer(t *testing.T, ttl time.Duration) *auth.JWTIssuer {
	t.Helper()
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret: []byte(testJWTSecret), Issuer: "t", KeyID: "v1", AccessTTL: ttl,
	})
	require.NoError(t, err)
	return iss
}

// probe wraps RequireAuth with a trivial handler that echoes the user_id
// from the context so tests can assert the middleware populated it.
func probe(iss *auth.JWTIssuer) *gin.Engine {
	r := gin.New()
	r.GET("/me", middleware.RequireAuth(iss), func(c *gin.Context) {
		cl := ginctx.Claims(c)
		if cl == nil {
			c.String(http.StatusInternalServerError, "no claims")
			return
		}
		c.String(http.StatusOK, cl.UserID)
	})
	return r
}

func TestRequireAuth_AcceptsValidToken(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t, 15*time.Minute)

	tok, err := iss.IssueAccess(auth.AccessClaims{UserID: "01ABC", Email: "e@x", IsAdmin: false})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	probe(iss).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "01ABC", rec.Body.String())
}

func TestRequireAuth_RejectsMissingHeader(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t, 15*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rec := httptest.NewRecorder()
	probe(iss).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_authorization")
}

func TestRequireAuth_RejectsMalformedHeader(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t, 15*time.Minute)

	cases := []string{"notbearer xyz", "Bearer", "Bearer ", "Basic abc"}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/me", nil)
			req.Header.Set("Authorization", h)
			rec := httptest.NewRecorder()
			probe(iss).ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

func TestRequireAuth_RejectsExpired(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t, -1*time.Hour) // already expired

	tok, err := iss.IssueAccess(auth.AccessClaims{UserID: "u", Email: "e@x"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	probe(iss).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireAuth_RejectsTampered(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t, 15*time.Minute)

	tok, err := iss.IssueAccess(auth.AccessClaims{UserID: "u", Email: "e@x"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	// Flip a char in the signature segment.
	req.Header.Set("Authorization", "Bearer "+tok+"x")
	rec := httptest.NewRecorder()
	probe(iss).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
