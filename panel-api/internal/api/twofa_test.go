package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/twofa"
)

// ---------- fakes specific to /2fa/* handler tests ----------

type fakeBackupCodeRepo struct {
	mu      sync.Mutex
	rows    map[string]*models.TOTPBackupCode // id → row
	byUser  map[string][]string               // userID → ids
	lastErr error
}

func newFakeBackupCodeRepo() *fakeBackupCodeRepo {
	return &fakeBackupCodeRepo{rows: map[string]*models.TOTPBackupCode{}, byUser: map[string][]string{}}
}

func (f *fakeBackupCodeRepo) CreateBatch(_ context.Context, codes []models.TOTPBackupCode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastErr != nil {
		return f.lastErr
	}
	for i := range codes {
		c := codes[i]
		f.rows[c.ID] = &c
		f.byUser[c.UserID] = append(f.byUser[c.UserID], c.ID)
	}
	return nil
}

func (f *fakeBackupCodeRepo) ListUnusedByUserID(_ context.Context, userID string) ([]models.TOTPBackupCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []models.TOTPBackupCode
	for _, id := range f.byUser[userID] {
		r := f.rows[id]
		if r != nil && r.UsedAt == nil {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeBackupCodeRepo) MarkUsed(_ context.Context, id string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return nil
	}
	r.UsedAt = &now
	return nil
}

func (f *fakeBackupCodeRepo) DeleteAllByUserID(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.byUser[userID] {
		delete(f.rows, id)
	}
	delete(f.byUser, userID)
	return nil
}

func (f *fakeBackupCodeRepo) count(userID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, id := range f.byUser[userID] {
		if r := f.rows[id]; r != nil && r.UsedAt == nil {
			n++
		}
	}
	return n
}

// ---------- helpers ----------

func makeSSOKey(t *testing.T) *ssokey.Key {
	t.Helper()
	var k ssokey.Key
	if _, err := rand.Read(k[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return &k
}

// setupTOTPRouter mounts /2fa/* under a throwaway /api/v1 group. Claims for
// the caller are injected via middleware — tests with userID=="" get no
// claims and should see 401.
func setupTOTPRouter(t *testing.T, userID string, users *mockUserRepo, backups *fakeBackupCodeRepo, key *ssokey.Key) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{UserID: userID})
			c.Next()
		})
	}
	RegisterTOTPRoutes(v1, TOTPHandlerConfig{
		Users:       users,
		BackupCodes: backups,
		SSOKey:      key,
	})
	return r
}

// doJSON POSTs payload to path and returns the response.
func doJSON(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- /2fa/enroll ----------

func TestTOTPEnroll_HappyPath(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "alice@example.com"},
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/enroll", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp enrollResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Secret, "secret must be set")
	assert.Contains(t, resp.OtpauthURL, "otpauth://totp/")

	// The stored secret must round-trip through the key.
	u := users.users["u1"]
	require.NotNil(t, u.TOTPSecretEncrypted, "secret should be persisted")
	plain, err := key.Open(u.TOTPSecretEncrypted)
	require.NoError(t, err)
	assert.Equal(t, resp.Secret, string(plain))
	assert.False(t, u.TOTPEnabled, "enroll must NOT set totp_enabled")
}

func TestTOTPEnroll_Unauthorized(t *testing.T) {
	t.Parallel()
	r := setupTOTPRouter(t, "", &mockUserRepo{}, newFakeBackupCodeRepo(), makeSSOKey(t))
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/enroll", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestTOTPEnroll_Conflict_WhenAlreadyEnabled(t *testing.T) {
	t.Parallel()
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "a@b", TOTPEnabled: true},
	}}
	r := setupTOTPRouter(t, "u1", users, newFakeBackupCodeRepo(), makeSSOKey(t))

	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/enroll", nil)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// ---------- /2fa/verify ----------

// enrolUser runs the enrol step and returns the base32 secret the client
// would display to the user. Lets subsequent verify/regen-backup tests call
// into the handler chain instead of re-implementing the crypto path.
func enrolUser(t *testing.T, r http.Handler) string {
	t.Helper()
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/enroll", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp enrollResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Secret
}

func TestTOTPVerify_HappyPath(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "a@b"},
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	secret := enrolUser(t, r)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)

	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: code})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp verifyResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.BackupCodes, twofa.BackupCodeCount, "10 backup codes expected")
	for _, c := range resp.BackupCodes {
		assert.Len(t, c, twofa.BackupCodeDigits)
	}
	assert.True(t, users.users["u1"].TOTPEnabled, "verify must flip totp_enabled")
	assert.Equal(t, twofa.BackupCodeCount, backups.count("u1"))
}

func TestTOTPVerify_InvalidCode(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "a@b"},
	}}
	r := setupTOTPRouter(t, "u1", users, newFakeBackupCodeRepo(), key)

	_ = enrolUser(t, r)

	// Send a known-bad code — 000000 is astronomically unlikely to match
	// the random window of a fresh secret.
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: "000000"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, users.users["u1"].TOTPEnabled, "failed verify must not enable")
}

func TestTOTPVerify_NoPendingEnrolment(t *testing.T) {
	t.Parallel()
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1"}, // no TOTPSecretEncrypted
	}}
	r := setupTOTPRouter(t, "u1", users, newFakeBackupCodeRepo(), makeSSOKey(t))

	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: "123456"})
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestTOTPVerify_BadRequest(t *testing.T) {
	t.Parallel()
	users := &mockUserRepo{users: map[string]*models.User{"u1": {ID: "u1"}}}
	r := setupTOTPRouter(t, "u1", users, newFakeBackupCodeRepo(), makeSSOKey(t))

	// Missing code
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Wrong length
	w = doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: "123"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------- /2fa/regen-backup ----------

func TestTOTPRegenBackup_HappyPath(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "a@b"},
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	// enrol → verify to get into enabled state
	secret := enrolUser(t, r)
	code, _ := totp.GenerateCode(secret, time.Now())
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: code})
	require.Equal(t, http.StatusOK, w.Code)

	initialCount := backups.count("u1")
	require.Equal(t, twofa.BackupCodeCount, initialCount)

	// regen with a valid TOTP code
	newCode, _ := totp.GenerateCode(secret, time.Now())
	w = doJSON(t, r, http.MethodPost, "/api/v1/2fa/regen-backup", regenBackupRequest{Code: newCode})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp regenBackupResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.BackupCodes, twofa.BackupCodeCount)
	assert.Equal(t, twofa.BackupCodeCount, backups.count("u1"),
		"regen should wipe old + insert N — count should still be N")
}

func TestTOTPRegenBackup_RequiresEnabled(t *testing.T) {
	t.Parallel()
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1"}, // not enabled
	}}
	r := setupTOTPRouter(t, "u1", users, newFakeBackupCodeRepo(), makeSSOKey(t))

	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/regen-backup", regenBackupRequest{Code: "123456"})
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestTOTPRegenBackup_InvalidCode(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": {ID: "u1", Email: "a@b"},
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	secret := enrolUser(t, r)
	code, _ := totp.GenerateCode(secret, time.Now())
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: code})
	require.Equal(t, http.StatusOK, w.Code)

	w = doJSON(t, r, http.MethodPost, "/api/v1/2fa/regen-backup", regenBackupRequest{Code: "000000"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, twofa.BackupCodeCount, backups.count("u1"),
		"failed regen must not mutate existing backup codes")
}

// ---------- /2fa/disable ----------

// newUserWithPassword returns a user row whose PasswordHash matches plain.
// Uses the auth package's hash so the real VerifyPassword path runs.
func newUserWithPassword(t *testing.T, id, email, plain string) *models.User {
	t.Helper()
	h, err := auth.HashPassword(plain, 4) // bcrypt min cost — tests are hot path
	require.NoError(t, err)
	return &models.User{ID: id, Email: email, PasswordHash: h}
}

func TestTOTPDisable_HappyPath(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": newUserWithPassword(t, "u1", "a@b", "correct horse battery staple"),
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	secret := enrolUser(t, r)
	code, _ := totp.GenerateCode(secret, time.Now())
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: code})
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, twofa.BackupCodeCount, backups.count("u1"))
	require.True(t, users.users["u1"].TOTPEnabled)

	disableCode, _ := totp.GenerateCode(secret, time.Now())
	w = doJSON(t, r, http.MethodPost, "/api/v1/2fa/disable", disableRequest{
		Password: "correct horse battery staple",
		Code:     disableCode,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	u := users.users["u1"]
	assert.False(t, u.TOTPEnabled)
	assert.Nil(t, u.TOTPEnabledAt)
	assert.Nil(t, u.TOTPSecretEncrypted, "disable must wipe the secret")
	assert.Equal(t, 0, backups.count("u1"), "disable must wipe backup codes")
}

func TestTOTPDisable_WrongPassword(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": newUserWithPassword(t, "u1", "a@b", "right"),
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	secret := enrolUser(t, r)
	code, _ := totp.GenerateCode(secret, time.Now())
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: code})
	require.Equal(t, http.StatusOK, w.Code)

	disableCode, _ := totp.GenerateCode(secret, time.Now())
	w = doJSON(t, r, http.MethodPost, "/api/v1/2fa/disable", disableRequest{
		Password: "wrong",
		Code:     disableCode,
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, users.users["u1"].TOTPEnabled, "failed disable must leave 2FA on")
	assert.Equal(t, twofa.BackupCodeCount, backups.count("u1"), "failed disable must not wipe codes")
}

func TestTOTPDisable_WrongCode(t *testing.T) {
	t.Parallel()
	key := makeSSOKey(t)
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": newUserWithPassword(t, "u1", "a@b", "right"),
	}}
	backups := newFakeBackupCodeRepo()
	r := setupTOTPRouter(t, "u1", users, backups, key)

	secret := enrolUser(t, r)
	code, _ := totp.GenerateCode(secret, time.Now())
	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/verify", verifyRequest{Code: code})
	require.Equal(t, http.StatusOK, w.Code)

	w = doJSON(t, r, http.MethodPost, "/api/v1/2fa/disable", disableRequest{
		Password: "right",
		Code:     "000000",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, users.users["u1"].TOTPEnabled)
}

func TestTOTPDisable_RequiresEnabled(t *testing.T) {
	t.Parallel()
	users := &mockUserRepo{users: map[string]*models.User{
		"u1": newUserWithPassword(t, "u1", "a@b", "pw"),
	}}
	r := setupTOTPRouter(t, "u1", users, newFakeBackupCodeRepo(), makeSSOKey(t))

	w := doJSON(t, r, http.MethodPost, "/api/v1/2fa/disable", disableRequest{Password: "pw", Code: "123456"})
	assert.Equal(t, http.StatusConflict, w.Code)
}

// smoke-test that ids.NewULID stays available across the package test
// fixtures (failure here means someone renamed the helper — catch fast).
func TestHelperAlive(t *testing.T) {
	t.Parallel()
	assert.NotEmpty(t, ids.NewULID())
}
