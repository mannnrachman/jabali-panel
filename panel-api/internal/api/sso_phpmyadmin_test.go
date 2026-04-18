package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/sso"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// generateTestKeySSOPhpMyAdmin creates a random 32-byte AES-256 key for testing.
func generateTestKeySSOPhpMyAdmin(t *testing.T) ssokey.Key {
	var k ssokey.Key
	if _, err := rand.Read(k[:]); err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}
	return k
}

// TestSSO_IssueToken_FirstClick tests the happy path: first SSO token issue.
// Ensures shadow account is provisioned and token is minted.
// NOTE: This test requires a real database (GORM transaction) and is actually
// an integration test. See integration_test.go for the actual implementation.
// This stub is kept for documentation purposes only.
func TestSSO_IssueToken_FirstClick(t *testing.T) {
	t.Skip("Requires integration test setup with real database")
	key := generateTestKeySSOPhpMyAdmin(t)

	mockDBs := &mockDatabaseRepo{
		databases: []models.Database{
			{
				ID:     "db1",
				Name:   "testdb",
				UserID: "user1",
			},
		},
	}

	mockUsers := &mockUserRepo{
		users: map[string]*models.User{
			"user1": {
				ID:       "user1",
				Username: ptrString("testuser"),
			},
		},
	}

	mockAgent := &mockAgent{
		callFn: func(ctx context.Context, command string, params any) (json.RawMessage, error) {
			return json.Marshal(map[string]interface{}{
				"mysqladmin_username": "testuser_mysqladmin",
				"mysqladmin_password": "secret123",
			})
		},
	}

	mockTokens := &mockSSOTokenRepo{}

	ssoService := sso.NewService(nil, mockUsers, mockTokens, mockAgent, &key, slog.Default())

	cfg := SSOPhpMyAdminHandlerConfig{
		Databases: mockDBs,
		SSO:       ssoService,
		Log:       slog.Default(),
	}

	h := &ssoPhpMyAdminHandler{cfg: cfg}

	// Create request with JWT claims
	body := ssoPhpMyAdminRequest{DatabaseID: "db1"}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/sso/phpmyadmin", bytes.NewReader(bodyJSON))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", "https://localhost:8443")

	// Inject JWT claims
	claims := &auth.AccessClaims{
		UserID: "user1",
	}
	ginctx.SetClaims(c, claims)

	h.issueSSOToken(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ssoPhpMyAdminResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp.RedirectURL, "/phpmyadmin/sso.php?")
	assert.Contains(t, resp.RedirectURL, "db=testdb")
}

// TestSSO_IssueToken_SecondClick tests the idempotent path: SSO token on second click.
// Shadow account already exists; no agent call.
// NOTE: This test requires a real database (GORM transaction) and is actually
// an integration test. See integration_test.go for the actual implementation.
// This stub is kept for documentation purposes only.
func TestSSO_IssueToken_SecondClick(t *testing.T) {
	t.Skip("Requires integration test setup with real database")
	key := generateTestKeySSOPhpMyAdmin(t)

	mockDBs := &mockDatabaseRepo{
		databases: []models.Database{
			{
				ID:     "db1",
				Name:   "testdb",
				UserID: "user1",
			},
		},
	}

	encryptedPwd, _ := key.Seal([]byte("secret123"))
	mockUsers := &mockUserRepo{
		users: map[string]*models.User{
			"user1": {
				ID:                      "user1",
				Username:                ptrString("testuser"),
				MysqladminUsername:      ptrString("testuser_mysqladmin"),
				MysqladminPasswordEnc:   encryptedPwd,
				MysqladminProvisionedAt: ptrTime(time.Now()),
			},
		},
	}

	mockAgent := &mockAgent{
		callCount: 0,
	}

	mockTokens := &mockSSOTokenRepo{}

	ssoService := sso.NewService(nil, mockUsers, mockTokens, mockAgent, &key, slog.Default())

	cfg := SSOPhpMyAdminHandlerConfig{
		Databases: mockDBs,
		SSO:       ssoService,
		Log:       slog.Default(),
	}

	h := &ssoPhpMyAdminHandler{cfg: cfg}

	body := ssoPhpMyAdminRequest{DatabaseID: "db1"}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/sso/phpmyadmin", bytes.NewReader(bodyJSON))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", "https://localhost:8443")

	claims := &auth.AccessClaims{
		UserID: "user1",
	}
	ginctx.SetClaims(c, claims)

	h.issueSSOToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, mockAgent.callCount, "agent should not be called when shadow account exists")
}

// TestSSO_IssueToken_NotAuthorized tests ownership check: 403 if database doesn't belong to user.
func TestSSO_IssueToken_NotAuthorized(t *testing.T) {
	key := generateTestKeySSOPhpMyAdmin(t)

	mockDBs := &mockDatabaseRepo{
		databases: []models.Database{
			{
				ID:     "db1",
				Name:   "testdb",
				UserID: "user2", // Different owner
			},
		},
	}

	mockUsers := &mockUserRepo{
		users: map[string]*models.User{
			"user1": {
				ID:       "user1",
				Username: ptrString("testuser"),
			},
		},
	}

	mockAgent := &mockAgent{}
	mockTokens := &mockSSOTokenRepo{}

	ssoService := sso.NewService(nil, mockUsers, mockTokens, mockAgent, &key, slog.Default())

	cfg := SSOPhpMyAdminHandlerConfig{
		Databases: mockDBs,
		SSO:       ssoService,
		Log:       slog.Default(),
	}

	h := &ssoPhpMyAdminHandler{cfg: cfg}

	body := ssoPhpMyAdminRequest{DatabaseID: "db1"}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/sso/phpmyadmin", bytes.NewReader(bodyJSON))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", "https://localhost:8443")

	claims := &auth.AccessClaims{
		UserID: "user1",
	}
	ginctx.SetClaims(c, claims)

	h.issueSSOToken(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestSSO_IssueToken_CrossOrigin tests CSRF rejection for cross-origin requests.
func TestSSO_IssueToken_CrossOrigin(t *testing.T) {
	key := generateTestKeySSOPhpMyAdmin(t)

	mockDBs := &mockDatabaseRepo{
		databases: []models.Database{
			{
				ID:     "db1",
				Name:   "testdb",
				UserID: "user1",
			},
		},
	}

	mockUsers := &mockUserRepo{}
	mockAgent := &mockAgent{}
	mockTokens := &mockSSOTokenRepo{}

	ssoService := sso.NewService(nil, mockUsers, mockTokens, mockAgent, &key, slog.Default())

	cfg := SSOPhpMyAdminHandlerConfig{
		Databases: mockDBs,
		SSO:       ssoService,
		Log:       slog.Default(),
	}

	h := &ssoPhpMyAdminHandler{cfg: cfg}

	body := ssoPhpMyAdminRequest{DatabaseID: "db1"}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/sso/phpmyadmin", bytes.NewReader(bodyJSON))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", "https://attacker.com") // Cross-origin!

	claims := &auth.AccessClaims{UserID: "user1"}
	ginctx.SetClaims(c, claims)

	h.issueSSOToken(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestSSO_IssueToken_NoAuth tests missing JWT.
func TestSSO_IssueToken_NoAuth(t *testing.T) {
	key := generateTestKeySSOPhpMyAdmin(t)

	mockDBs := &mockDatabaseRepo{}
	mockUsers := &mockUserRepo{}
	mockAgent := &mockAgent{}
	mockTokens := &mockSSOTokenRepo{}

	ssoService := sso.NewService(nil, mockUsers, mockTokens, mockAgent, &key, slog.Default())

	cfg := SSOPhpMyAdminHandlerConfig{
		Databases: mockDBs,
		SSO:       ssoService,
		Log:       slog.Default(),
	}

	h := &ssoPhpMyAdminHandler{cfg: cfg}

	body := ssoPhpMyAdminRequest{DatabaseID: "db1"}
	bodyJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/sso/phpmyadmin", bytes.NewReader(bodyJSON))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Origin", "https://localhost:8443")
	// No claims set

	h.issueSSOToken(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// Helper functions

func ptrString(s string) *string {
	return &s
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

// Mock implementations for SSO tests

type mockSSOTokenRepo struct {
	created []*models.PhpMyAdminSSOToken
}

func (m *mockSSOTokenRepo) Create(ctx context.Context, token *models.PhpMyAdminSSOToken) error {
	m.created = append(m.created, token)
	return nil
}

func (m *mockSSOTokenRepo) ConsumeByHash(ctx context.Context, hash string) (*models.PhpMyAdminSSOToken, error) {
	return nil, repository.ErrNotFound
}

func (m *mockSSOTokenRepo) PurgeExpired(ctx context.Context) (int64, error) {
	return 0, nil
}
