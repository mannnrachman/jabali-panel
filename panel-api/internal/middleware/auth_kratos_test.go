package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// fakeUsersRepo is a minimal stand-in for repository.UserRepository that
// only answers FindByKratosIdentityID — the single method the Kratos
// middleware calls. Other methods panic so a mistaken call surfaces as a
// loud test failure rather than a silent default.
type fakeUsersRepo struct {
	byKratosID map[string]*models.User
}

func (f *fakeUsersRepo) FindByKratosIdentityID(_ context.Context, kratosID string) (*models.User, error) {
	u, ok := f.byKratosID[kratosID]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return u, nil
}

// The other repository methods are intentionally unimplemented — middleware
// must never reach them, and a panic advertises a regression loudly.
func (f *fakeUsersRepo) Create(context.Context, *models.User) error {
	panic("Create not expected from middleware")
}
func (f *fakeUsersRepo) FindByID(context.Context, string) (*models.User, error) {
	panic("FindByID not expected from middleware")
}
func (f *fakeUsersRepo) FindByEmail(context.Context, string) (*models.User, error) {
	panic("FindByEmail not expected from middleware")
}
func (f *fakeUsersRepo) FindByUsername(context.Context, string) (*models.User, error) {
	panic("FindByUsername not expected from middleware")
}
func (f *fakeUsersRepo) List(context.Context, repository.ListOptions) ([]models.User, int64, error) {
	panic("List not expected from middleware")
}
func (f *fakeUsersRepo) Update(context.Context, *models.User) error {
	panic("Update not expected from middleware")
}
func (f *fakeUsersRepo) LinkKratosIdentity(context.Context, string, string) error {
	panic("LinkKratosIdentity not expected from middleware")
}
func (f *fakeUsersRepo) SetAdmin(context.Context, string, bool) error {
	panic("SetAdmin not expected from middleware")
}
func (f *fakeUsersRepo) CountAdmins(context.Context) (int64, error) {
	panic("CountAdmins not expected from middleware")
}
func (f *fakeUsersRepo) FindAdminsByEmail(context.Context) ([]*models.User, error) {
	panic("FindAdminsByEmail not expected from middleware")
}
func (f *fakeUsersRepo) Delete(context.Context, string) error {
	panic("Delete not expected from middleware")
}
func (f *fakeUsersRepo) SetTOTPSecret(context.Context, string, []byte) error {
	panic("SetTOTPSecret not expected from middleware")
}
func (f *fakeUsersRepo) EnableTOTP(context.Context, string, time.Time) error {
	panic("EnableTOTP not expected from middleware")
}
func (f *fakeUsersRepo) DisableTOTP(context.Context, string) error {
	panic("DisableTOTP not expected from middleware")
}

// kratosProbe mounts RequireKratosSession with a probe handler that echoes
// the authenticated user's panel id, email, and is_admin flag.
func kratosProbe(client *kratosclient.Client, users repository.UserRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/me", middleware.RequireKratosSession(client, users), func(c *gin.Context) {
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

// fakeKratos stands in for the Kratos public server in tests. status controls
// the HTTP code /sessions/whoami returns. body is the response JSON.
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

// seededRepo returns a fakeUsersRepo pre-seeded with users indexed by their
// Kratos identity ID.
func seededRepo(users ...*models.User) repository.UserRepository {
	f := &fakeUsersRepo{byKratosID: map[string]*models.User{}}
	for _, u := range users {
		if u.KratosIdentityID != nil {
			f.byKratosID[*u.KratosIdentityID] = u
		}
	}
	return f
}

func ptrString(s string) *string { return &s }

func TestRequireKratosSession_ValidCookie_ResolvesToPanelUser(t *testing.T) {
	t.Parallel()
	// Real Kratos /sessions/whoami returns a Session envelope; the nested
	// identity.id is what matches users.kratos_identity_id. The top-level
	// `id` is the SESSION id and must be ignored by the decoder.
	identityJSON := `{
		"id": "session-uuid-ignore",
		"active": true,
		"identity": {
			"id": "kratos-uuid-01",
			"traits": {"email": "user@example.com", "username": "alice", "is_admin": true}
		}
	}`
	srv := fakeKratos(t, http.StatusOK, identityJSON)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo(&models.User{
		ID:               "01PANEL-ULID-ABC",
		Email:            "user@example.com",
		IsAdmin:          true,
		KratosIdentityID: ptrString("kratos-uuid-01"),
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid"})
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	// Critical invariant: claims.UserID is the PANEL ULID, not the Kratos UUID.
	// If this regresses, every ownership check in the API silently 403s.
	assert.Contains(t, rec.Body.String(), `"user_id":"01PANEL-ULID-ABC"`)
	assert.NotContains(t, rec.Body.String(), "kratos-uuid-01")
}

func TestRequireKratosSession_PanelIsAdminOverridesKratosTrait(t *testing.T) {
	t.Parallel()
	// Kratos trait says is_admin=true but the panel row says false — the panel
	// row wins so an admin demotion takes effect on next request even if the
	// Kratos identity trait hasn't been updated yet.
	identityJSON := `{
		"id": "session-uuid-ignore",
		"active": true,
		"identity": {
			"id": "kratos-uuid-02",
			"traits": {"email": "alice@x", "is_admin": true}
		}
	}`
	srv := fakeKratos(t, http.StatusOK, identityJSON)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo(&models.User{
		ID:               "01PANEL-ULID-DEMOTED",
		Email:            "alice@x",
		IsAdmin:          false,
		KratosIdentityID: ptrString("kratos-uuid-02"),
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid"})
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"is_admin":false`)
}

func TestRequireKratosSession_IdentityNotLinked_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK, `{"id":"sess","active":true,"identity":{"id":"kratos-orphan","traits":{"email":"e@x"}}}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	// Empty repo — identity exists in Kratos but has no panel user.
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid"})
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "identity_not_linked")
}

func TestRequireKratosSession_MissingCookie_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK, `{}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

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
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	// No Kratos cookie, but a Bearer header is present — should be ignored.
	req.Header.Set("Authorization", "Bearer some.jwt.token")
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing_session")
}

func TestRequireKratosSession_KratosReturns401_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusUnauthorized, `{"error":"no active session"}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "stale-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

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
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid-looking-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

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
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid-token"})
	rec := httptest.NewRecorder()

	kratosProbe(client, repo).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "identity_service_unavailable")
}

// redirectProbe mounts RequireKratosSessionOrRedirect on /guarded
// and echoes the authenticated user on success. Used by the
// browser-flow redirect tests below.
func redirectProbe(client *kratosclient.Client, users repository.UserRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/guarded", middleware.RequireKratosSessionOrRedirect(client, users, "/login"),
		func(c *gin.Context) {
			cl := ginctx.Claims(c)
			c.JSON(http.StatusOK, gin.H{"user_id": cl.UserID})
		})
	return r
}

// TestRequireKratosSessionOrRedirect_MissingCookie_Redirects pins the
// "first-time SSO" browser flow: a user lands on /oauth2-login via
// Hydra with no panel session yet. A 401 would strand them; the
// browser variant must emit a 302 to /login with a return_to that
// preserves the full original URL so the SPA can bounce back after
// login.
func TestRequireKratosSessionOrRedirect_MissingCookie_Redirects(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK, `{}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/guarded?login_challenge=abc", nil)
	rec := httptest.NewRecorder()

	redirectProbe(client, repo).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Contains(t, loc, "/login?return_to=")
	// return_to must round-trip the full original path+query so the
	// SPA can POST /login and then bounce back to exactly the URL
	// that triggered the redirect.
	assert.Contains(t, loc, "login_challenge%3Dabc")
}

// TestRequireKratosSessionOrRedirect_InvalidSession_Redirects covers
// the same browser-flow outcome when Kratos returns 401 (expired
// session, revoked token). Same 302, not a 401 blob.
func TestRequireKratosSessionOrRedirect_InvalidSession_Redirects(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusUnauthorized, `{"error":"no active session"}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo()

	req := httptest.NewRequest(http.MethodGet, "/guarded", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "expired-token"})
	rec := httptest.NewRecorder()

	redirectProbe(client, repo).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/login?return_to=")
}

// TestRequireKratosSessionOrRedirect_ValidSession_AllowsThrough
// sanity-check: with a real session the handler runs normally and
// claims are populated.
func TestRequireKratosSessionOrRedirect_ValidSession_AllowsThrough(t *testing.T) {
	t.Parallel()
	srv := fakeKratos(t, http.StatusOK,
		`{"id":"sess","identity":{"id":"kratos-uuid-01","traits":{"email":"u@x.com"}}}`)
	defer srv.Close()

	client := kratosclient.NewClient(srv.URL, srv.URL)
	repo := seededRepo(&models.User{
		ID:               "01PANEL-REDIR",
		Email:            "u@x.com",
		KratosIdentityID: ptrString("kratos-uuid-01"),
	})

	req := httptest.NewRequest(http.MethodGet, "/guarded", nil)
	req.AddCookie(&http.Cookie{Name: "ory_kratos_session", Value: "valid-token"})
	rec := httptest.NewRecorder()

	redirectProbe(client, repo).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"user_id":"01PANEL-REDIR"`)
}
