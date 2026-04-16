package api_test

import (
		"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// memRefreshTokenRepo is a tiny in-memory RefreshTokenRepository for testing.
type memRefreshTokenRepo struct {
	tokens map[string]*models.RefreshToken
}

func newMemRefreshTokenRepo() *memRefreshTokenRepo {
	return &memRefreshTokenRepo{tokens: make(map[string]*models.RefreshToken)}
}

func (m *memRefreshTokenRepo) Create(_ context.Context, tok *models.RefreshToken) error {
	if _, exists := m.tokens[tok.TokenHash]; exists {
		return repository.ErrConflict
	}
	c := *tok
	m.tokens[tok.TokenHash] = &c
	return nil
}

func (m *memRefreshTokenRepo) FindByHash(_ context.Context, hash string) (*models.RefreshToken, error) {
	if tok, ok := m.tokens[hash]; ok {
		c := *tok
		return &c, nil
	}
	return nil, repository.ErrNotFound
}

func (m *memRefreshTokenRepo) Rotate(_ context.Context, oldHash string, newTok *models.RefreshToken) error {
	if _, exists := m.tokens[oldHash]; !exists {
		return repository.ErrNotFound
	}
	c := *newTok
	m.tokens[newTok.TokenHash] = &c
	now := time.Now().UTC()
	old := m.tokens[oldHash]
	old.RevokedAt = &now
	return nil
}

func (m *memRefreshTokenRepo) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	for _, tok := range m.tokens {
		if tok.ID == id {
			tok.RevokedAt = &revokedAt
			return nil
		}
	}
	return repository.ErrNotFound
}

func (m *memRefreshTokenRepo) RevokeAllForUser(_ context.Context, userID string, at time.Time) error {
	for _, tok := range m.tokens {
		if tok.UserID == userID {
			
			tok.RevokedAt = &at
		}
	}
	return nil
}

// buildRouterForImpersonation wires a Gin engine with user routes, auth service, and
// JWT issuer configured for impersonation tests.
func buildRouterForImpersonation(
	t *testing.T,
	userRepo *memUserRepo,
	refreshRepo *memRefreshTokenRepo,
	claims *auth.AccessClaims,
) (*gin.Engine, *auth.Service, *auth.JWTIssuer) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Set up JWT issuer
	jwtIssuer, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret: []byte("test-key-that-is-long-enough-for-hs256-minimum-32-bytes"),
		Issuer: "test-issuer",
		KeyID: "test-key",
		AccessTTL: 15 * time.Minute,
	})
	require.NoError(t, err)

	// Set up auth service with refresh repo
	authSvc := auth.NewService(auth.ServiceConfig{
		Users:       userRepo,
		RefreshRepo: refreshRepo,
		JWT:         jwtIssuer,
		BcryptCost:  bcrypt.MinCost,
		RefreshTTL:  24 * time.Hour,
	})

	// Set up logger
	logger := slog.Default()

	g := r.Group("/api/v1", func(c *gin.Context) {
		if claims != nil {
			ginctx.SetClaims(c, claims)
		}
		c.Next()
	})

	// Admin group for impersonate endpoint
	_ = g.Group("/admin", func(c *gin.Context) {
		// Check if user is admin
		cl := ginctx.Claims(c)
		if cl == nil || !cl.IsAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			c.Abort()
			return
		}
		c.Next()
	})

	// Register routes with full config
	api.RegisterUserRoutes(g, api.UserHandlerConfig{
		Repo:            userRepo,
		Agent:           nil,
		StrictRateLimit: nil,
		Domains:         nil,
		Packages:        nil,
		Reconciler:      nil,
		AuthService:     authSvc, // Concrete auth.Service type
		AccessTTL:       15 * time.Minute,
		RefreshTTL:      24 * time.Hour,
		CookieName:      "refresh_token",
		CookieSecure:    false,
		Log:             logger,
		BcryptCost:      bcrypt.MinCost,
	})

	return r, authSvc, jwtIssuer
}

// ---------- tests ----------

func TestImpersonate_HappyPath(t *testing.T) {
	t.Parallel()

	userRepo := newMemUserRepo()
	refreshRepo := newMemRefreshTokenRepo()

	adminUser := makeUser(t, "admin@example.com", true, "password123")
	targetUser := makeUser(t, "user@example.com", false, "password456")

	userRepo.seed(adminUser)
	userRepo.seed(targetUser)

	r, _, jwtIssuer := buildRouterForImpersonation(
		t,
		userRepo,
		refreshRepo,
		&auth.AccessClaims{
			UserID:  adminUser.ID,
			Email:   adminUser.Email,
			IsAdmin: true,
		},
	)

	// POST /api/v1/admin/users/:id/impersonate
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/users/"+targetUser.ID+"/impersonate",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	// Verify response contains access token and expires_at
	assert.NotEmpty(t, resp["access_token"])
	assert.NotEmpty(t, resp["expires_at"])

	// Parse the token to verify claims
	accessToken := resp["access_token"].(string)
	claims, err := jwtIssuer.Verify(accessToken)
	require.NoError(t, err)

	// Verify impersonated_by claim is set
	assert.Equal(t, targetUser.ID, claims.UserID)
	assert.Equal(t, targetUser.Email, claims.Email)
	assert.False(t, claims.IsAdmin)
	assert.Equal(t, adminUser.ID, claims.ImpersonatedBy)

	// Verify refresh cookie is set
	cookies := w.Result().Cookies()
	var refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "refresh_token" {
			refreshCookie = c
			break
		}
	}
	assert.NotNil(t, refreshCookie, "refresh_token cookie should be set")
	assert.NotEmpty(t, refreshCookie.Value)
}

func TestImpersonate_NonAdminForbidden(t *testing.T) {
	t.Parallel()

	userRepo := newMemUserRepo()
	refreshRepo := newMemRefreshTokenRepo()

	user1 := makeUser(t, "user1@example.com", false, "password123")
	user2 := makeUser(t, "user2@example.com", false, "password456")

	userRepo.seed(user1)
	userRepo.seed(user2)

	r, _, _ := buildRouterForImpersonation(
		t,
		userRepo,
		refreshRepo,
		&auth.AccessClaims{
			UserID:  user1.ID,
			Email:   user1.Email,
			IsAdmin: false,
		},
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/users/"+user2.ID+"/impersonate",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "forbidden", resp["error"])
}

func TestImpersonate_TargetNotFound(t *testing.T) {
	t.Parallel()

	userRepo := newMemUserRepo()
	refreshRepo := newMemRefreshTokenRepo()

	adminUser := makeUser(t, "admin@example.com", true, "password123")
	userRepo.seed(adminUser)

	r, _, _ := buildRouterForImpersonation(
		t,
		userRepo,
		refreshRepo,
		&auth.AccessClaims{
			UserID:  adminUser.ID,
			Email:   adminUser.Email,
			IsAdmin: true,
		},
	)

	nonexistentID := ids.NewULID()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/users/"+nonexistentID+"/impersonate",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "not_found", resp["error"])
}

func TestImpersonate_CannotImpersonateAdmin(t *testing.T) {
	t.Parallel()

	userRepo := newMemUserRepo()
	refreshRepo := newMemRefreshTokenRepo()

	admin1 := makeUser(t, "admin1@example.com", true, "password123")
	admin2 := makeUser(t, "admin2@example.com", true, "password456")

	userRepo.seed(admin1)
	userRepo.seed(admin2)

	r, _, _ := buildRouterForImpersonation(
		t,
		userRepo,
		refreshRepo,
		&auth.AccessClaims{
			UserID:  admin1.ID,
			Email:   admin1.Email,
			IsAdmin: true,
		},
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/users/"+admin2.ID+"/impersonate",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "cannot_impersonate_admin", resp["error"])
}

func TestImpersonate_CannotImpersonateSelf(t *testing.T) {
	t.Parallel()

	userRepo := newMemUserRepo()
	refreshRepo := newMemRefreshTokenRepo()

	adminUser := makeUser(t, "admin@example.com", true, "password123")
	userRepo.seed(adminUser)

	r, _, _ := buildRouterForImpersonation(
		t,
		userRepo,
		refreshRepo,
		&auth.AccessClaims{
			UserID:  adminUser.ID,
			Email:   adminUser.Email,
			IsAdmin: true,
		},
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/users/"+adminUser.ID+"/impersonate",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "cannot_impersonate_self", resp["error"])
}

// TestImpersonatedBy_PreservedInRefresh verifies that impersonated_by claim
// persists when a token is refreshed.
func TestImpersonatedBy_PreservedInRefresh(t *testing.T) {
	t.Parallel()

	userRepo := newMemUserRepo()
	refreshRepo := newMemRefreshTokenRepo()

	adminUser := makeUser(t, "admin@example.com", true, "password123")
	targetUser := makeUser(t, "user@example.com", false, "password456")

	userRepo.seed(adminUser)
	userRepo.seed(targetUser)

	_, authSvc, jwtIssuer := buildRouterForImpersonation(
		t,
		userRepo,
		refreshRepo,
		&auth.AccessClaims{
			UserID:  adminUser.ID,
			Email:   adminUser.Email,
			IsAdmin: true,
		},
	)

	// Issue impersonation token
	impersonationOutput, err := authSvc.IssueImpersonation(context.Background(), targetUser, adminUser.ID)
	require.NoError(t, err)

	// Verify impersonated_by is in the initial access token
	accessClaims, err := jwtIssuer.Verify(impersonationOutput.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, adminUser.ID, accessClaims.ImpersonatedBy)

	// Now refresh the token
	refreshOutput, err := authSvc.Refresh(context.Background(), auth.RefreshInput{
		RawRefresh: impersonationOutput.RawRefresh,
		DeviceID:   adminUser.ID,
	})
	require.NoError(t, err)

	// Verify impersonated_by is still in the new access token
	newAccessClaims, err := jwtIssuer.Verify(refreshOutput.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, targetUser.ID, newAccessClaims.UserID)
	assert.Equal(t, targetUser.Email, newAccessClaims.Email)
	assert.False(t, newAccessClaims.IsAdmin)
	assert.Equal(t, adminUser.ID, newAccessClaims.ImpersonatedBy, "impersonated_by should persist after refresh")
}

// TestMe_ExposesImpersonatedBy verifies that GET /api/v1/me includes impersonated_by
// when the caller is impersonated.
func TestMe_ExposesImpersonatedBy(t *testing.T) {
	t.Parallel()

	adminUser := makeUser(t, "admin@example.com", true, "password123")
	targetUser := makeUser(t, "user@example.com", false, "password456")

	userRepo := newMemUserRepo()
	userRepo.seed(adminUser)
	userRepo.seed(targetUser)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1", func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{
			UserID:         targetUser.ID,
			Email:          targetUser.Email,
			IsAdmin:        targetUser.IsAdmin,
			ImpersonatedBy: adminUser.ID,
		})
		c.Next()
	})

	api.RegisterMeRoutes(v1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, targetUser.ID, resp["id"])
	assert.Equal(t, targetUser.Email, resp["email"])
	assert.Equal(t, false, resp["is_admin"])
	assert.Equal(t, adminUser.ID, resp["impersonated_by"], "impersonated_by should be exposed in /me response")
}

// TestMe_OmitsImpersonatedByWhenNotImpersonated verifies that GET /api/v1/me
// does NOT include impersonated_by when the caller is not impersonated.
func TestMe_OmitsImpersonatedByWhenNotImpersonated(t *testing.T) {
	t.Parallel()

	user := makeUser(t, "user@example.com", false, "password123")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1", func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{
			UserID:  user.ID,
			Email:   user.Email,
			IsAdmin: user.IsAdmin,
		})
		c.Next()
	})

	api.RegisterMeRoutes(v1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, user.ID, resp["id"])
	assert.Equal(t, user.Email, resp["email"])
	assert.Equal(t, false, resp["is_admin"])
	_, exists := resp["impersonated_by"]
	assert.False(t, exists, "impersonated_by should not be present when not impersonated")
}
