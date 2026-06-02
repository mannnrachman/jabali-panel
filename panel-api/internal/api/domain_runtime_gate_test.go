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

// setupRuntimeGateRouter wires just the domain PATCH route with a mock
// domain repo so we can exercise the ADR-0113 runtime_type validation
// and the docker admin gate without standing up the full app.
func setupRuntimeGateRouter(t *testing.T, claims auth.AccessClaims) (*gin.Engine, *mockDomainRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &claims)
		c.Next()
	})
	domains := newMockDomainRepo()
	RegisterDomainRoutes(r.Group("/api/v1"), DomainHandlerConfig{Domains: domains})
	return r, domains
}

func patchRuntimeType(t *testing.T, r *gin.Engine, id, rt string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"runtime_type": rt})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUpdateRuntimeType_NonAdminDockerForbidden(t *testing.T) {
	r, domains := setupRuntimeGateRouter(t, auth.AccessClaims{UserID: "u1", IsAdmin: false})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})

	w := patchRuntimeType(t, r, "d1", models.RuntimeDocker)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin docker should be 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := domains.domains["d1"].RuntimeType; got == models.RuntimeDocker {
		t.Fatalf("runtime_type must not have changed to docker for non-admin")
	}
}

func TestUpdateRuntimeType_AdminDockerAllowed(t *testing.T) {
	r, domains := setupRuntimeGateRouter(t, auth.AccessClaims{UserID: "admin", IsAdmin: true})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})

	w := patchRuntimeType(t, r, "d1", models.RuntimeDocker)
	if w.Code != http.StatusOK {
		t.Fatalf("admin docker should be 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := domains.domains["d1"].RuntimeType; got != models.RuntimeDocker {
		t.Fatalf("runtime_type should be docker, got %q", got)
	}
}

func TestUpdateRuntimeType_NonAdminNodejsAllowed(t *testing.T) {
	r, domains := setupRuntimeGateRouter(t, auth.AccessClaims{UserID: "u1"})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})

	w := patchRuntimeType(t, r, "d1", models.RuntimeNodeJS)
	if w.Code != http.StatusOK {
		t.Fatalf("non-admin nodejs should be 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := domains.domains["d1"].RuntimeType; got != models.RuntimeNodeJS {
		t.Fatalf("runtime_type should be nodejs, got %q", got)
	}
}

func TestUpdateRuntimeType_Invalid(t *testing.T) {
	r, domains := setupRuntimeGateRouter(t, auth.AccessClaims{UserID: "u1"})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com"})

	w := patchRuntimeType(t, r, "d1", "perl")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime_type should be 400, got %d: %s", w.Code, w.Body.String())
	}
}

func postCreateDomain(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The runtime gate on create runs before the user lookup, so these
// rejection cases need only the domain mock.
func TestCreateRuntimeType_InvalidRejected(t *testing.T) {
	r, _ := setupRuntimeGateRouter(t, auth.AccessClaims{UserID: "u1"})
	w := postCreateDomain(t, r, map[string]any{"name": "new.example.com", "runtime_type": "perl"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime_type on create should be 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRuntimeType_NonAdminDockerForbidden(t *testing.T) {
	r, _ := setupRuntimeGateRouter(t, auth.AccessClaims{UserID: "u1", IsAdmin: false})
	w := postCreateDomain(t, r, map[string]any{"name": "new.example.com", "runtime_type": models.RuntimeDocker})
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin docker on create should be 403, got %d: %s", w.Code, w.Body.String())
	}
}

type mockRuntimeServiceRepoGate struct{}

func (m *mockRuntimeServiceRepoGate) Create(ctx context.Context, s *models.RuntimeService) error {
	return nil
}
func (m *mockRuntimeServiceRepoGate) FindByID(ctx context.Context, id string) (*models.RuntimeService, error) {
	return nil, context.Canceled
}
func (m *mockRuntimeServiceRepoGate) FindByDomainID(ctx context.Context, domainID string) (*models.RuntimeService, error) {
	return &models.RuntimeService{ID: "rt1", DomainID: domainID}, nil
}
func (m *mockRuntimeServiceRepoGate) FindByUserID(ctx context.Context, userID string) ([]models.RuntimeService, error) {
	return nil, nil
}
func (m *mockRuntimeServiceRepoGate) ListAll(ctx context.Context, opts repository.ListOptions) ([]models.RuntimeService, int64, error) {
	return nil, 0, nil
}
func (m *mockRuntimeServiceRepoGate) ListByStatus(ctx context.Context, status string) ([]models.RuntimeService, error) {
	return nil, nil
}
func (m *mockRuntimeServiceRepoGate) Update(ctx context.Context, s *models.RuntimeService) error {
	return nil
}
func (m *mockRuntimeServiceRepoGate) Delete(ctx context.Context, id string) error { return nil }
func (m *mockRuntimeServiceRepoGate) SetStatus(ctx context.Context, id, status string, lastErr *string) error {
	return nil
}
func (m *mockRuntimeServiceRepoGate) IsPortInUse(ctx context.Context, port uint32) (bool, error) {
	return false, nil
}

func setupDockerRuntimeRouteHarness(t *testing.T, claims auth.AccessClaims) (*gin.Engine, *mockDomainRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &claims)
		c.Next()
	})
	domains := newMockDomainRepo()
	RegisterDomainRoutes(r.Group("/api/v1"), DomainHandlerConfig{Domains: domains, RuntimeServices: &mockRuntimeServiceRepoGate{}})
	return r, domains
}

func TestDockerRuntimeDetails_NonAdminForbidden(t *testing.T) {
	r, domains := setupDockerRuntimeRouteHarness(t, auth.AccessClaims{UserID: "u1", IsAdmin: false})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com", RuntimeType: models.RuntimeDocker})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/d1/runtime", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin docker runtime GET should be 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDockerRuntimePatch_NonAdminForbidden(t *testing.T) {
	r, domains := setupDockerRuntimeRouteHarness(t, auth.AccessClaims{UserID: "u1", IsAdmin: false})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com", RuntimeType: models.RuntimeDocker})

	body, _ := json.Marshal(map[string]any{"entry_point": "alpine:latest"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1/runtime", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin docker runtime PATCH should be 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDockerRuntimeRestart_NonAdminForbidden(t *testing.T) {
	r, domains := setupDockerRuntimeRouteHarness(t, auth.AccessClaims{UserID: "u1", IsAdmin: false})
	domains.Create(context.Background(), &models.Domain{ID: "d1", UserID: "u1", Name: "example.com", RuntimeType: models.RuntimeDocker})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/d1/runtime/restart", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin docker runtime restart should be 403, got %d: %s", w.Code, w.Body.String())
	}
}
