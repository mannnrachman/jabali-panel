package api

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func strPtr(s string) *string { return &s }


// WordPress-specific mock repositories

type mockWordPressInstallRepo struct {
	mu       sync.RWMutex
	installs map[string]*models.WordPressInstall
	byDomain map[string]*models.WordPressInstall
}

func (m *mockWordPressInstallRepo) Create(ctx context.Context, inst *models.WordPressInstall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installs == nil {
		m.installs = make(map[string]*models.WordPressInstall)
	}
	if m.byDomain == nil {
		m.byDomain = make(map[string]*models.WordPressInstall)
	}
	m.installs[inst.ID] = inst
	m.byDomain[inst.DomainID] = inst
	return nil
}

func (m *mockWordPressInstallRepo) FindByID(ctx context.Context, id string) (*models.WordPressInstall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if inst, ok := m.installs[id]; ok {
		return inst, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockWordPressInstallRepo) FindByIDAndUserID(ctx context.Context, id, userID string) (*models.WordPressInstall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.installs[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	if inst.UserID != userID {
		return nil, repository.ErrNotFound
	}
	return inst, nil
}

func (m *mockWordPressInstallRepo) FindByDomainID(ctx context.Context, domainID string) (*models.WordPressInstall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if inst, ok := m.byDomain[domainID]; ok {
		return inst, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockWordPressInstallRepo) FindByDomainAndSubdirectory(ctx context.Context, domainID, subdirectory string) (*models.WordPressInstall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.installs {
		if inst.DomainID == domainID && inst.Subdirectory == subdirectory {
			return inst, nil
		}
	}
	return nil, repository.ErrNotFound
}

// FindByDomainAndSubdirectoryAndAppType — added for the M19 generalisation.
// Pre-M19 rows had no AppType column; the model defaults to "wordpress" so
// the legacy WP test fixtures match an app_type="wordpress" query without
// each test having to set the field explicitly.
func (m *mockWordPressInstallRepo) FindByDomainAndSubdirectoryAndAppType(ctx context.Context, domainID, subdirectory, appType string) (*models.WordPressInstall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.installs {
		instAppType := inst.AppType
		if instAppType == "" {
			instAppType = "wordpress"
		}
		if inst.DomainID == domainID && inst.Subdirectory == subdirectory && instAppType == appType {
			return inst, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockWordPressInstallRepo) FindByDBID(ctx context.Context, dbID string) (*models.WordPressInstall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.installs {
		if inst.DBID == dbID {
			return inst, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockWordPressInstallRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.WordPressInstall, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []models.WordPressInstall
	for _, inst := range m.installs {
		result = append(result, *inst)
	}
	return result, int64(len(result)), nil
}

func (m *mockWordPressInstallRepo) ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.WordPressInstall, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []models.WordPressInstall
	for _, inst := range m.installs {
		if inst.UserID == userID {
			result = append(result, *inst)
		}
	}
	return result, int64(len(result)), nil
}

func (m *mockWordPressInstallRepo) UpdateStatus(ctx context.Context, id, status string, lastError *string, version *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.installs[id]; ok {
		inst.Status = status
		if lastError != nil {
			inst.LastError = *lastError
		}
		inst.Version = version
		inst.UpdatedAt = time.Now()
		return nil
	}
	return repository.ErrNotFound
}

func (m *mockWordPressInstallRepo) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.installs[id]; ok {
		delete(m.byDomain, inst.DomainID)
		delete(m.installs, id)
		return nil
	}
	return repository.ErrNotFound
}

// GetByIDUnsafe returns the install without locking (for tests that need quick reads)
// This is only safe if called immediately after an operation or if the caller ensures no concurrent access
func (m *mockWordPressInstallRepo) GetByIDUnsafe(id string) *models.WordPressInstall {
	if inst, ok := m.installs[id]; ok {
		return inst
	}
	return nil
}

// Test helper

func wordPressRouter(userID string, isAdmin bool, wpRepo *mockWordPressInstallRepo, domainRepo *mockDomainRepo, dbRepo *mockDatabaseRepo, userRepo *mockUserRepo, pkgRepo *mockPackageRepo, ag *mockAgent) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{
				UserID:  userID,
				IsAdmin: isAdmin,
			})
			c.Next()
		})
	}

	cfg := WordPressHandlerConfig{
		WordPressInstalls: wpRepo,
		Databases:         dbRepo,
		DatabaseUsers:     &mockDatabaseUserRepo{},
		DatabaseGrants:    &mockDatabaseGrantRepo{},
		Domains:           domainRepo,
		Users:             userRepo,
		Packages:          pkgRepo,
		Agent:             ag,
	}
	RegisterWordPressRoutes(v1, cfg)

	return r
}

// Database grant mock for WordPress tests

type mockDatabaseGrantRepo struct {
	grants map[string]*models.DatabaseUserGrant
}

func (m *mockDatabaseGrantRepo) Create(ctx context.Context, g *models.DatabaseUserGrant) error {
	if m.grants == nil {
		m.grants = make(map[string]*models.DatabaseUserGrant)
	}
	m.grants[g.ID] = g
	return nil
}

func (m *mockDatabaseGrantRepo) Delete(ctx context.Context, id string) error {
	if _, ok := m.grants[id]; ok {
		delete(m.grants, id)
		return nil
	}
	return repository.ErrNotFound
}

func (m *mockDatabaseGrantRepo) FindByID(ctx context.Context, id string) (*models.DatabaseUserGrant, error) {
	if g, ok := m.grants[id]; ok {
		return g, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockDatabaseGrantRepo) ListByDatabaseID(ctx context.Context, databaseID string) ([]models.DatabaseUserGrant, error) {
	return nil, nil
}

func (m *mockDatabaseGrantRepo) ListByDatabaseUserID(ctx context.Context, databaseUserID string) ([]models.DatabaseUserGrant, error) {
	return nil, nil
}

func (m *mockDatabaseGrantRepo) ListByDatabaseUserIDs(ctx context.Context, databaseUserIDs []string) ([]models.DatabaseUserGrant, error) {
	return nil, nil
}

func (m *mockDatabaseGrantRepo) UpdateLevel(ctx context.Context, id string, level string) error {
	return nil
}

func (m *mockDatabaseGrantRepo) UpdatePrivileges(ctx context.Context, id string, privileges string) error {
	return nil
}

func (m *mockDatabaseGrantRepo) FindByDBAndDBUser(ctx context.Context, databaseID string, databaseUserID string) (*models.DatabaseUserGrant, error) {
	return nil, repository.ErrNotFound
}

// Tests

func TestWordPressCreateHappyPath(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {
				ID:        "domain1",
				UserID:    "user1",
				Name:      "example.com",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{
		users: map[string]*models.User{
			"user1": {ID: "user1", Username: strPtr("testuser")},
		},
	}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	body := createWordPressRequest{
		DomainID:      "domain1",
		SiteTitle:     "My Site",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d", http.StatusAccepted, w.Code)
	}

	var resp createWordPressResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Status != "pending" {
		t.Fatalf("expected pending, got %s", resp.Status)
	}
	if resp.AdminPassword == "" {
		t.Fatal("expected admin_password in response")
	}
}

func TestWordPressCreateDuplicateDomainConflict(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {
				ID:       "inst1",
				UserID:   "user1",
				DomainID: "domain1",
				Status:   "ready",
			},
		},
		byDomain: map[string]*models.WordPressInstall{
			"domain1": {
				ID:       "inst1",
				UserID:   "user1",
				DomainID: "domain1",
				Status:   "ready",
			},
		},
	}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {
				ID:     "domain1",
				UserID: "user1",
				Name:   "example.com",
			},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1"}}}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	body := createWordPressRequest{
		DomainID:      "domain1",
		SiteTitle:     "My Site",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected %d, got %d", http.StatusConflict, w.Code)
	}
}

func TestWordPressListAdminSeesAll(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Status: "ready"},
			"inst2": {ID: "inst2", UserID: "user2", DomainID: "domain2", Status: "ready"},
		},
	}
	domainRepo := &mockDomainRepo{}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("admin1", true, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	req := httptest.NewRequest("GET", "/api/v1/wordpress-installs", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"] != float64(2) {
		t.Fatalf("expected total=2, got %v", resp["total"])
	}
}

func TestWordPressGetOwnershipCheck404(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Status: "ready"},
		},
	}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {ID: "domain1", UserID: "user1"},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user2", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	// user2 trying to access user1's install
	req := httptest.NewRequest("GET", "/api/v1/wordpress-installs/inst1", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	// Should be 404, not 403
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected %d (404), got %d", http.StatusNotFound, w.Code)
	}
}

func TestWordPressDeleteSuccess(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", DBID: "db1", Status: "ready"},
		},
	}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {ID: "domain1", UserID: "user1", DocRoot: "/home/testuser/domains/example.com/public_html"},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1", Username: strPtr("testuser")}}}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	req := httptest.NewRequest("DELETE", "/api/v1/wordpress-installs/inst1", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d", http.StatusAccepted, w.Code)
	}

	// Check status was updated to 'deleting'
	// Note: we read immediately after the handler returns, before async goroutine completes
	wpRepo.mu.RLock()
	inst := wpRepo.installs["inst1"]
	wpRepo.mu.RUnlock()
	
	if inst == nil {
		t.Fatal("install was deleted before test could verify status")
	}
	if inst.Status != "deleting" {
		t.Fatalf("expected status=deleting, got %s", inst.Status)
	}
}

func TestWordPressCloneCrossDomainOwnershipCheck(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Status: "ready"},
		},
	}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {ID: "domain1", UserID: "user1"},
			"domain2": {ID: "domain2", UserID: "user2"}, // Different owner
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	body := cloneWordPressRequest{
		DestDomainID: "domain2", // user2's domain
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs/inst1/clone", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected %d (403), got %d", http.StatusForbidden, w.Code)
	}
}

func TestWordPressCloneDestinationConflict(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Status: "ready"},
			"inst2": {ID: "inst2", UserID: "user1", DomainID: "domain2", Status: "ready"},
		},
		byDomain: map[string]*models.WordPressInstall{
			"domain1": {ID: "inst1", UserID: "user1", DomainID: "domain1"},
			"domain2": {ID: "inst2", UserID: "user1", DomainID: "domain2"},
		},
	}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {ID: "domain1", UserID: "user1"},
			"domain2": {ID: "domain2", UserID: "user1"},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	body := cloneWordPressRequest{
		DestDomainID: "domain2", // Already has install
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs/inst1/clone", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected %d, got %d", http.StatusConflict, w.Code)
	}
}

func TestWordPressHealthStub(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{
		installs: map[string]*models.WordPressInstall{
			"inst1": {ID: "inst1", UserID: "user1", DomainID: "domain1", Status: "ready"},
		},
	}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {ID: "domain1", UserID: "user1"},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs/inst1/health", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, w.Code)
	}

	var resp healthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.WPInstalled != false {
		t.Fatal("expected wp_installed=false in stub")
	}
}

func TestWordPressCreateUnauthenticated(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	// No auth middleware configured — claims will be nil
	r := wordPressRouter("", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	body := createWordPressRequest{
		DomainID:      "domain1",
		SiteTitle:     "My Site",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestWordPressCreateInvalidEmail(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{
		domains: map[string]*models.Domain{
			"domain1": {ID: "domain1", UserID: "user1"},
		},
	}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1"}}}
	pkgRepo := &mockPackageRepo{}
	ag := &mockAgent{}

	r := wordPressRouter("user1", false, wpRepo, domainRepo, dbRepo, userRepo, pkgRepo, ag)

	body := createWordPressRequest{
		DomainID:      "domain1",
		SiteTitle:     "My Site",
		AdminUsername: "admin",
		AdminEmail:    "not-an-email",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// ----------------------------------------------------------------------------
// Regression tests for WordPress install/clone fixes landed 2026-04-18.
// Each block has a **Why** comment pointing at the commit that introduced it
// so a future rewrite doesn't silently regress any of these four fires.
// ----------------------------------------------------------------------------

// Why ef5ab63: grant_level was briefly "all" but the DB enum is only 'rw'/'ro'.
// This locked us into a 500 on every WP install. Enforce that the install path
// writes "rw" so the column accepts it.
func TestWordPressCreateWritesRWGrantLevel(t *testing.T) {
	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{domains: map[string]*models.Domain{
		"domain1": {ID: "domain1", UserID: "user1", Name: "example.com", DocRoot: "/home/testuser/example.com/public_html"},
	}}
	dbRepo := &mockDatabaseRepo{}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1", Username: strPtr("testuser")}}}
	grantRepo := &mockDatabaseGrantRepo{}
	cfg := WordPressHandlerConfig{
		WordPressInstalls: wpRepo, Databases: dbRepo,
		DatabaseUsers: &mockDatabaseUserRepo{}, DatabaseGrants: grantRepo,
		Domains: domainRepo, Users: userRepo, Packages: &mockPackageRepo{}, Agent: &mockAgent{},
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "user1", IsAdmin: false})
		c.Next()
	})
	RegisterWordPressRoutes(r.Group("/api/v1"), cfg)

	body, _ := json.Marshal(createWordPressRequest{
		DomainID: "domain1", SiteTitle: "x", AdminUsername: "admin", AdminEmail: "a@b.com",
	})
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if len(grantRepo.grants) != 1 {
		t.Fatalf("expected 1 grant created, got %d", len(grantRepo.grants))
	}
	for _, g := range grantRepo.grants {
		if g.GrantLevel != "rw" {
			t.Fatalf("grant_level must be 'rw' (enum constraint), got %q", g.GrantLevel)
		}
	}
}

// Why ba17cd7: the install row was being written without ever calling agent
// db.create + db_user.create + db_user.grant, so wp core install bombed with
// "Error establishing a database connection". These three calls MUST happen
// synchronously before the install goroutine is spawned.
func TestWordPressCreateProvisionsMariaDBViaAgentInOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	ag := &mockAgent{callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
		mu.Lock(); defer mu.Unlock()
		calls = append(calls, cmd)
		return json.RawMessage(`{}`), nil
	}}
	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{domains: map[string]*models.Domain{
		"domain1": {ID: "domain1", UserID: "user1", Name: "example.com", DocRoot: "/home/testuser/example.com/public_html"},
	}}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1", Username: strPtr("testuser")}}}
	cfg := WordPressHandlerConfig{
		WordPressInstalls: wpRepo, Databases: &mockDatabaseRepo{},
		DatabaseUsers: &mockDatabaseUserRepo{}, DatabaseGrants: &mockDatabaseGrantRepo{},
		Domains: domainRepo, Users: userRepo, Packages: &mockPackageRepo{}, Agent: ag,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "user1", IsAdmin: false})
		c.Next()
	})
	RegisterWordPressRoutes(r.Group("/api/v1"), cfg)

	body, _ := json.Marshal(createWordPressRequest{
		DomainID: "domain1", SiteTitle: "x", AdminUsername: "admin", AdminEmail: "a@b.com",
	})
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	// Agent.Call may race with the background install goroutine. Take the
	// first three calls (provisioning is synchronous + before goroutine)
	// and assert exact order.
	mu.Lock(); defer mu.Unlock()
	if len(calls) < 3 {
		t.Fatalf("expected at least 3 agent calls (db.create, db_user.create, db_user.grant), got %d: %v", len(calls), calls)
	}
	want := []string{"db.create", "db_user.create", "db_user.grant"}
	for i, w := range want {
		if calls[i] != w {
			t.Fatalf("agent call[%d] = %q, want %q; full=%v", i, calls[i], w, calls)
		}
	}
}

// Why d367187 + 762c7fe: the original DB name used ULID head (timestamp head
// of Crockford base32, deterministic per-minute) AND uppercase — so back-to-
// back installs collided and the agent's lowercase-only regex rejected both.
// Fix: take the random tail + lowercase it.
func TestWordPressCreateDBNameIsLowerCasePrefixedRandom(t *testing.T) {
	dbRepo := &mockDatabaseRepo{}
	wpRepo := &mockWordPressInstallRepo{}
	domainRepo := &mockDomainRepo{domains: map[string]*models.Domain{
		"domain1": {ID: "domain1", UserID: "user1", Name: "example.com", DocRoot: "/home/testuser/example.com/public_html"},
	}}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1", Username: strPtr("testuser")}}}
	cfg := WordPressHandlerConfig{
		WordPressInstalls: wpRepo, Databases: dbRepo,
		DatabaseUsers: &mockDatabaseUserRepo{}, DatabaseGrants: &mockDatabaseGrantRepo{},
		Domains: domainRepo, Users: userRepo, Packages: &mockPackageRepo{}, Agent: &mockAgent{},
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "user1", IsAdmin: false})
		c.Next()
	})
	RegisterWordPressRoutes(r.Group("/api/v1"), cfg)

	body, _ := json.Marshal(createWordPressRequest{
		DomainID: "domain1", SiteTitle: "x", AdminUsername: "admin", AdminEmail: "a@b.com",
	})
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if len(dbRepo.databases) != 1 {
		t.Fatalf("expected 1 database row, got %d", len(dbRepo.databases))
	}
	var name string
	for _, d := range dbRepo.databases { name = d.Name }
	// Must be prefixed by the linux username.
	if !startsWith(name, "testuser_wp_") {
		t.Fatalf("db name must start with linux user prefix, got %q", name)
	}
	// Must be all lowercase (agent regex before 762c7fe was lowercase-only;
	// tests must pin lowercase so we don't regress into the old bug where
	// Crockford uppercase chars leaked through).
	for _, ch := range name {
		if ch >= 'A' && ch <= 'Z' {
			t.Fatalf("db name must be all-lowercase, got %q (uppercase %c)", name, ch)
		}
	}
}

// Why this PR: the clone path was missing the exact agent provisioning block
// that install got in ba17cd7. A clone today would fail the same way install
// did this morning. Mirror the same ordered-calls assertion on the clone path.
func TestWordPressCloneProvisionsMariaDBViaAgentInOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	ag := &mockAgent{callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
		mu.Lock(); defer mu.Unlock()
		calls = append(calls, cmd)
		return json.RawMessage(`{}`), nil
	}}
	sourceInstall := &models.WordPressInstall{
		ID: "srcInstall", UserID: "user1", DomainID: "sourceDomain", DBID: "srcDB",
		AdminUsername: "admin", AdminEmail: "a@b.com", Locale: "en_US", Status: "ready",
	}
	wpRepo := &mockWordPressInstallRepo{installs: map[string]*models.WordPressInstall{"srcInstall": sourceInstall}}
	domainRepo := &mockDomainRepo{domains: map[string]*models.Domain{
		"sourceDomain": {ID: "sourceDomain", UserID: "user1", Name: "src.com", DocRoot: "/home/testuser/src/public_html"},
		"destDomain":   {ID: "destDomain",   UserID: "user1", Name: "dst.com", DocRoot: "/home/testuser/dst/public_html"},
	}}
	userRepo := &mockUserRepo{users: map[string]*models.User{"user1": {ID: "user1", Username: strPtr("testuser")}}}
	cfg := WordPressHandlerConfig{
		WordPressInstalls: wpRepo, Databases: &mockDatabaseRepo{},
		DatabaseUsers: &mockDatabaseUserRepo{}, DatabaseGrants: &mockDatabaseGrantRepo{},
		Domains: domainRepo, Users: userRepo, Packages: &mockPackageRepo{}, Agent: ag,
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "user1", IsAdmin: false})
		c.Next()
	})
	RegisterWordPressRoutes(r.Group("/api/v1"), cfg)

	body, _ := json.Marshal(cloneWordPressRequest{DestDomainID: "destDomain"})
	req := httptest.NewRequest("POST", "/api/v1/wordpress-installs/srcInstall/clone", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	mu.Lock(); defer mu.Unlock()
	if len(calls) < 3 {
		t.Fatalf("clone must call db.create/db_user.create/db_user.grant before kicking wordpress.clone; got %d calls: %v", len(calls), calls)
	}
	want := []string{"db.create", "db_user.create", "db_user.grant"}
	for i, w := range want {
		if calls[i] != w {
			t.Fatalf("clone agent call[%d] = %q, want %q; full=%v", i, calls[i], w, calls)
		}
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
