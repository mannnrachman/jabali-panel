package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// injectClaims is a test-only middleware that pre-populates AccessClaims
// so RBAC tests don't need a real JWT.
func injectClaims(cl *auth.AccessClaims) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cl != nil {
			ginctx.SetClaims(c, cl)
		}
		c.Next()
	}
}

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.GET("/a",
		injectClaims(&auth.AccessClaims{UserID: "u", IsAdmin: true}),
		middleware.RequireAdmin(),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireAdmin_RejectsNonAdmin(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.GET("/a",
		injectClaims(&auth.AccessClaims{UserID: "u", IsAdmin: false}),
		middleware.RequireAdmin(),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "forbidden")
}

func TestRequireAdmin_RejectsMissingClaims(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.GET("/a",
		injectClaims(nil), // no claims on context
		middleware.RequireAdmin(),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Missing claims means RequireAuth hasn't run — safest is 401.
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireOwner_AllowsOwner(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.GET("/u/:id",
		injectClaims(&auth.AccessClaims{UserID: "01U", IsAdmin: false}),
		middleware.RequireOwner("id"),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	req := httptest.NewRequest(http.MethodGet, "/u/01U", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireOwner_AllowsAdminForAnyID(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.GET("/u/:id",
		injectClaims(&auth.AccessClaims{UserID: "01A", IsAdmin: true}),
		middleware.RequireOwner("id"),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	req := httptest.NewRequest(http.MethodGet, "/u/01SOMEONE_ELSE", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireOwner_RejectsOtherUser(t *testing.T) {
	t.Parallel()

	r := gin.New()
	r.GET("/u/:id",
		injectClaims(&auth.AccessClaims{UserID: "01U", IsAdmin: false}),
		middleware.RequireOwner("id"),
		func(c *gin.Context) { c.String(http.StatusOK, "ok") },
	)

	req := httptest.NewRequest(http.MethodGet, "/u/01OTHER", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
