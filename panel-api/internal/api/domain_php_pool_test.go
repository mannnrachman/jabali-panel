package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	ginctx "git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// mockPHPPoolRepo is a minimal in-memory PHPPoolRepository used by the
// domain↔pool binding tests. It implements only the methods the bind
// handler touches; unused methods return zero values.
type mockPHPPoolRepo struct {
	pools map[string]*models.PHPPool
}

func newMockPHPPoolRepo() *mockPHPPoolRepo {
	return &mockPHPPoolRepo{pools: make(map[string]*models.PHPPool)}
}

func (m *mockPHPPoolRepo) Create(ctx context.Context, p *models.PHPPool) error {
	m.pools[p.ID] = p
	return nil
}

func (m *mockPHPPoolRepo) FindByID(ctx context.Context, id string) (*models.PHPPool, error) {
	if p, ok := m.pools[id]; ok {
		return p, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockPHPPoolRepo) FindByUserID(ctx context.Context, userID string) (*models.PHPPool, error) {
	for _, p := range m.pools {
		if p.UserID == userID {
			return p, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockPHPPoolRepo) ListAll(ctx context.Context, opts repository.ListOptions) ([]models.PHPPool, int64, error) {
	return nil, 0, nil
}

func (m *mockPHPPoolRepo) Update(ctx context.Context, p *models.PHPPool) error {
	m.pools[p.ID] = p
	return nil
}

func (m *mockPHPPoolRepo) Delete(ctx context.Context, id string) error {
	delete(m.pools, id)
	return nil
}

func (m *mockPHPPoolRepo) SetStatus(ctx context.Context, id, status string, lastErr *string) error {
	if p, ok := m.pools[id]; ok {
		p.Status = status
	}
	return nil
}

func setupBindRouter(t *testing.T, claims auth.AccessClaims) (*gin.Engine, *mockDomainRepo, *mockPHPPoolRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Inject claims into the gin context as the real auth middleware would.
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &claims)
		c.Next()
	})
	domains := newMockDomainRepo()
	pools := newMockPHPPoolRepo()
	RegisterDomainPHPPoolRoutes(r.Group("/api/v1"), DomainPHPPoolHandlerConfig{
		Domains:  domains,
		PHPPools: pools,
	})
	return r, domains, pools
}

func TestBindDomainPHPPool_ByPoolID(t *testing.T) {
	r, domains, pools := setupBindRouter(t, auth.AccessClaims{UserID: "u1", IsAdmin: false})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})
	pools.Create(context.Background(), &models.PHPPool{ID: "p1", UserID: "u1", PHPVersion: "8.3"})

	body, _ := json.Marshal(map[string]string{"pool_id": "p1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/d1/php-pool", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := domains.domains["d1"].PHPPoolID; got == nil || *got != "p1" {
		t.Fatalf("domain not bound to pool: %+v", got)
	}
}

func TestBindDomainPHPPool_ByPHPVersion_Match(t *testing.T) {
	r, domains, pools := setupBindRouter(t, auth.AccessClaims{UserID: "u1"})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})
	pools.Create(context.Background(), &models.PHPPool{ID: "p1", UserID: "u1", PHPVersion: "8.3"})

	body, _ := json.Marshal(map[string]string{"php_version": "8.3"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/d1/php-pool", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := domains.domains["d1"].PHPPoolID; got == nil || *got != "p1" {
		t.Fatalf("domain not bound: %+v", got)
	}
}

func TestBindDomainPHPPool_ByPHPVersion_Mismatch_UpdatesPool(t *testing.T) {
	// ADR-0023 constrains each user to exactly one pool, so a user-driven
	// version switch mutates that single pool in place rather than
	// rejecting the request. Bind succeeds and the pool's version flips.
	r, domains, pools := setupBindRouter(t, auth.AccessClaims{UserID: "u1"})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})
	pools.Create(context.Background(), &models.PHPPool{ID: "p1", UserID: "u1", PHPVersion: "8.3"})

	body, _ := json.Marshal(map[string]string{"php_version": "8.1"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/d1/php-pool", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := domains.domains["d1"].PHPPoolID; got == nil || *got != "p1" {
		t.Fatalf("domain not bound to existing pool: %+v", got)
	}
	if got := pools.pools["p1"].PHPVersion; got != "8.1" {
		t.Fatalf("pool php_version should be updated to 8.1, got %q", got)
	}
}

func TestBindDomainPHPPool_MissingBothFields(t *testing.T) {
	r, domains, pools := setupBindRouter(t, auth.AccessClaims{UserID: "u1"})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1"})
	_ = pools

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/d1/php-pool", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBindDomainPHPPool_BothFields(t *testing.T) {
	r, domains, pools := setupBindRouter(t, auth.AccessClaims{UserID: "u1"})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1"})
	pools.Create(context.Background(), &models.PHPPool{ID: "p1", UserID: "u1", PHPVersion: "8.3"})

	body, _ := json.Marshal(map[string]string{"pool_id": "p1", "php_version": "8.3"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/d1/php-pool", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 when both fields set, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUnbindDomainPHPPool(t *testing.T) {
	r, domains, pools := setupBindRouter(t, auth.AccessClaims{UserID: "u1"})
	poolID := "p1"
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", PHPPoolID: &poolID})
	pools.Create(context.Background(), &models.PHPPool{ID: poolID, UserID: "u1", PHPVersion: "8.3"})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/domains/d1/php-pool", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if domains.domains["d1"].PHPPoolID != nil {
		t.Fatalf("domain should be unbound")
	}
}
