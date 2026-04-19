package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/twofa"
)

// Wave C — full login→challenge integration, real auth.Service and real
// JWTIssuer. Storage is faked; the crypto, JWT, and 2FA-pending machinery
// run end-to-end.

// ---------- fake refresh-token repo (local copy; users_impersonate_test.go's version is in api_test) ----------

type memRTRepo struct {
	tokens map[string]*models.RefreshToken
}

func newMemRTRepo() *memRTRepo {
	return &memRTRepo{tokens: map[string]*models.RefreshToken{}}
}

func (m *memRTRepo) Create(_ context.Context, t *models.RefreshToken) error {
	if _, exists := m.tokens[t.TokenHash]; exists {
		return repository.ErrConflict
	}
	c := *t
	m.tokens[c.TokenHash] = &c
	return nil
}

func (m *memRTRepo) FindByHash(_ context.Context, h string) (*models.RefreshToken, error) {
	if t, ok := m.tokens[h]; ok {
		c := *t
		return &c, nil
	}
	return nil, repository.ErrNotFound
}

func (m *memRTRepo) Rotate(_ context.Context, oldHash string, newTok *models.RefreshToken) error {
	old, ok := m.tokens[oldHash]
	if !ok {
		return repository.ErrNotFound
	}
	now := time.Now().UTC()
	old.RevokedAt = &now
	c := *newTok
	m.tokens[c.TokenHash] = &c
	return nil
}

func (m *memRTRepo) Revoke(_ context.Context, id string, at time.Time) error {
	for _, t := range m.tokens {
		if t.ID == id {
			t.RevokedAt = &at
			return nil
		}
	}
	return repository.ErrNotFound
}

func (m *memRTRepo) RevokeAllForUser(_ context.Context, userID string, at time.Time) error {
	for _, t := range m.tokens {
		if t.UserID == userID {
			t.RevokedAt = &at
		}
	}
	return nil
}

// ---------- harness ----------

// setupLoginFlowRouter wires a real auth.Service (with real JWTIssuer) onto
// a fresh Gin engine with /auth/* mounted. Handler-to-service wiring,
// pending-token semantics, and cookie behaviour are all exercised.
func setupLoginFlowRouter(t *testing.T, users *mockUserRepo, backups *fakeBackupCodeRepo, key *ssokey.Key) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte("test-jwt-secret-that-is-long-enough-32-bytes!"),
		Issuer:    "jabali-test",
		KeyID:     "v1",
		AccessTTL: 15 * time.Minute,
	})
	require.NoError(t, err)

	svc := auth.NewService(auth.ServiceConfig{
		Users:           users,
		RefreshRepo:     newMemRTRepo(),
		JWT:             iss,
		BcryptCost:      4, // bcrypt.MinCost — keeps tests sub-second
		RefreshTTL:      24 * time.Hour,
		TOTPBackupCodes: backups,
		SSOKey:          key,
	})

	r := gin.New()
	RegisterAuthRoutes(r, AuthHandlerConfig{
		Service:    svc,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 24 * time.Hour,
		CookieName: "jabali_refresh",
	})
	return r
}

// seedEnrolledUser creates a user row with a hashed password, a TOTP secret
// sealed under key, and totp_enabled=true. Returns the user + the plaintext
// base32 secret so the test can mint valid codes.
func seedEnrolledUser(t *testing.T, users *mockUserRepo, key *ssokey.Key, email, password string) (*models.User, string) {
	t.Helper()
	pwHash, err := auth.HashPassword(password, 4)
	require.NoError(t, err)

	en, err := twofa.NewEnrolment(email)
	require.NoError(t, err)
	sealed, err := key.Seal([]byte(en.Secret))
	require.NoError(t, err)

	now := time.Now().UTC()
	u := &models.User{
		ID:                  ids.NewULID(),
		Email:               email,
		PasswordHash:        pwHash,
		TOTPSecretEncrypted: sealed,
		TOTPEnabled:         true,
		TOTPEnabledAt:       &now,
	}
	if users.users == nil {
		users.users = map[string]*models.User{}
	}
	users.users[u.ID] = u
	return u, en.Secret
}

// postJSON sends body to path and returns the recorder. Request helper
// — the doJSON in twofa_test.go works too, but that one is httptest.NewRequest
// based and this flavour gives us more control over headers for the 2fa
// challenge test.
func postJSON(t *testing.T, r http.Handler, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(body))
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- tests ----------

func TestLoginFlow_2FA_EndToEnd(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{}
	backups := newFakeBackupCodeRepo()
	r := setupLoginFlowRouter(t, users, backups, key)

	u, secret := seedEnrolledUser(t, users, key, "alice@example.com", "hunter2")

	// --- step 1: POST /login ---
	w := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"email": u.Email, "password": "hunter2"}, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &loginResp))
	assert.Equal(t, true, loginResp["twofa_pending"], "login must signal 2FA required")
	pendingTok, _ := loginResp["twofa_pending_token"].(string)
	require.NotEmpty(t, pendingTok, "pending token must be returned")

	// No access/refresh yet.
	_, hasAccess := loginResp["access_token"]
	assert.False(t, hasAccess, "access_token must NOT be issued before challenge passes")

	// Refresh cookie must NOT be set yet.
	for _, c := range w.Result().Cookies() {
		if c.Name == "jabali_refresh" {
			t.Fatalf("refresh cookie set before 2FA challenge: %+v", c)
		}
	}

	// --- step 2: POST /2fa/challenge with a valid TOTP ---
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	w = postJSON(t, r, "/api/v1/auth/2fa/challenge", map[string]string{
		"twofa_pending_token": pendingTok,
		"code":                code,
	}, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var challengeResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &challengeResp))
	access, _ := challengeResp["access_token"].(string)
	assert.NotEmpty(t, access, "access_token must be issued on successful challenge")

	// Refresh cookie now set.
	var rc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "jabali_refresh" {
			rc = c
		}
	}
	require.NotNil(t, rc, "refresh cookie must be set after challenge")
	assert.NotEmpty(t, rc.Value)
	assert.True(t, rc.HttpOnly)
}

func TestLoginFlow_2FA_InvalidChallengeCode(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{}
	backups := newFakeBackupCodeRepo()
	r := setupLoginFlowRouter(t, users, backups, key)

	u, _ := seedEnrolledUser(t, users, key, "bob@example.com", "pw")

	w := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"email": u.Email, "password": "pw"}, nil)
	require.Equal(t, http.StatusOK, w.Code)

	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &loginResp))
	pendingTok := loginResp["twofa_pending_token"].(string)

	// Send a known-bad 6-digit code.
	w = postJSON(t, r, "/api/v1/auth/2fa/challenge", map[string]string{
		"twofa_pending_token": pendingTok,
		"code":                "000000",
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// No cookie.
	for _, c := range w.Result().Cookies() {
		if c.Name == "jabali_refresh" {
			t.Fatalf("refresh cookie set on failed challenge: %+v", c)
		}
	}
}

func TestLoginFlow_2FA_BackupCodeRedeems(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{}
	backups := newFakeBackupCodeRepo()
	r := setupLoginFlowRouter(t, users, backups, key)

	u, _ := seedEnrolledUser(t, users, key, "carol@example.com", "pw")

	// Pre-seed a single backup code, bcrypt-hashed like the real flow.
	plainBackup := "12345678"
	hash, err := twofa.HashCode(plainBackup)
	require.NoError(t, err)
	require.NoError(t, backups.CreateBatch(context.Background(), []models.TOTPBackupCode{{
		ID:        ids.NewULID(),
		UserID:    u.ID,
		CodeHash:  hash,
		CreatedAt: time.Now().UTC(),
	}}))
	require.Equal(t, 1, backups.count(u.ID))

	// Step 1: login → pending token.
	w := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"email": u.Email, "password": "pw"}, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var loginResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &loginResp))
	pendingTok := loginResp["twofa_pending_token"].(string)

	// Step 2: challenge with backup code in the "backup_code" slot.
	w = postJSON(t, r, "/api/v1/auth/2fa/challenge", map[string]string{
		"twofa_pending_token": pendingTok,
		"backup_code":         plainBackup,
	}, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Backup code must be marked used (count of unused drops to 0).
	assert.Equal(t, 0, backups.count(u.ID), "redeemed backup code must be marked used")

	// Replay must fail.
	w = postJSON(t, r, "/api/v1/auth/2fa/challenge", map[string]string{
		"twofa_pending_token": pendingTok,
		"backup_code":         plainBackup,
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "already-used backup code must not redeem again")
}

func TestLoginFlow_NoPending_WhenNotEnrolled(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{}
	r := setupLoginFlowRouter(t, users, newFakeBackupCodeRepo(), key)

	// Seed a user WITHOUT TOTP enabled.
	pwHash, err := auth.HashPassword("pw", 4)
	require.NoError(t, err)
	u := &models.User{ID: ids.NewULID(), Email: "dave@example.com", PasswordHash: pwHash}
	users.users = map[string]*models.User{u.ID: u}

	w := postJSON(t, r, "/api/v1/auth/login",
		map[string]string{"email": u.Email, "password": "pw"}, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// No 2FA flag, direct access token issued.
	_, hasFlag := resp["twofa_pending"]
	assert.False(t, hasFlag, "users without TOTP must not see twofa_pending")
	assert.NotEmpty(t, resp["access_token"], "access_token must be issued directly")

	// Refresh cookie set.
	var rc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "jabali_refresh" {
			rc = c
		}
	}
	require.NotNil(t, rc, "refresh cookie must be set for non-2FA login")
}
