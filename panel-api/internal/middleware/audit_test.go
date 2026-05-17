package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

type fakeRec struct{ ch chan *models.AuditEvent }

func newFakeRec() *fakeRec { return &fakeRec{ch: make(chan *models.AuditEvent, 8)} }
func (f *fakeRec) Record(e *models.AuditEvent) { f.ch <- e }

func (f *fakeRec) recorded(t *testing.T) *models.AuditEvent {
	t.Helper()
	select {
	case e := <-f.ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("expected an audit event, got none")
		return nil
	}
}
func (f *fakeRec) none(t *testing.T) {
	t.Helper()
	select {
	case e := <-f.ch:
		t.Fatalf("expected NO audit event, got %q", e.Action)
	case <-time.After(100 * time.Millisecond):
	}
}

func newRouter(rec *fakeRec, claims *auth.AccessClaims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if claims != nil {
			ginctx.SetClaims(c, claims)
		}
		c.Next()
	})
	r.Use(middleware.AuditRecord(rec))
	v1 := r.Group("/api/v1")
	v1.GET("/users", func(c *gin.Context) { c.String(200, "ok") })
	v1.POST("/users", func(c *gin.Context) { c.String(201, "ok") })
	v1.PATCH("/users/:id", func(c *gin.Context) { c.String(200, "ok") })
	v1.DELETE("/users/:id", func(c *gin.Context) { c.Status(403) })
	v1.POST("/me/ssh-keys", func(c *gin.Context) { c.String(200, "ok") })
	return r
}

func do(r *gin.Engine, method, path string) {
	req := httptest.NewRequest(method, path, nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
}

func TestAudit_GETNotRecorded(t *testing.T) {
	rec := newFakeRec()
	r := newRouter(rec, &auth.AccessClaims{UserID: "u1"})
	do(r, http.MethodGet, "/api/v1/users")
	rec.none(t) // honest scope: reads are not audited
}

func TestAudit_MutationRecorded_RouteTemplateNotConcreteID(t *testing.T) {
	rec := newFakeRec()
	r := newRouter(rec, &auth.AccessClaims{UserID: "admin1", IsAdmin: true})
	do(r, http.MethodPatch, "/api/v1/users/01HZUSERCONCRETEID00000000")

	e := rec.recorded(t)
	require.Equal(t, "PATCH /api/v1/users/:id", e.Action, "route template, no high-cardinality id")
	require.Equal(t, models.AuditActorAdmin, e.ActorKind)
	require.NotNil(t, e.ActorUserID)
	require.Equal(t, "admin1", *e.ActorUserID)
	require.Equal(t, models.AuditResultOK, e.Result)
	require.Equal(t, "user", e.TargetType)
	require.Equal(t, "01HZUSERCONCRETEID00000000", e.TargetID)
	require.Nil(t, e.SubjectUserID, "cross-user :id is NOT generically attributed (admin-only by default)")
}

func TestAudit_ForbiddenMapsToDenied(t *testing.T) {
	rec := newFakeRec()
	r := newRouter(rec, &auth.AccessClaims{UserID: "u1"})
	do(r, http.MethodDelete, "/api/v1/users/x")
	e := rec.recorded(t)
	require.Equal(t, models.AuditResultDenied, e.Result)
	require.Equal(t, models.AuditActorUser, e.ActorKind)
}

func TestAudit_MeRouteIsSelfScoped(t *testing.T) {
	rec := newFakeRec()
	r := newRouter(rec, &auth.AccessClaims{UserID: "u9"})
	do(r, http.MethodPost, "/api/v1/me/ssh-keys")
	e := rec.recorded(t)
	require.NotNil(t, e.SubjectUserID, "/me/* is self-scoped → visible in the user's own activity")
	require.Equal(t, "u9", *e.SubjectUserID)
}

func TestAudit_NilRecorderIsPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.AuditRecord(nil)) // disabled
	r.POST("/x", func(c *gin.Context) { c.String(200, "ok") })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	require.Equal(t, 200, rec.Code) // no panic, handler ran
}
