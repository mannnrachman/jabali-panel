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
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// fakeManagedIPsForDomain is the in-memory ManagedIPRepository fake used
// by the M24 domain-handler tests. Mirrors the shape in
// reconciler/managed_ips_test.go but lives here because tests in
// internal/api/ can't import internal/reconciler test fixtures.
type fakeManagedIPsForDomain struct {
	rows []models.ManagedIP
}

func (f *fakeManagedIPsForDomain) Create(ctx context.Context, ip *models.ManagedIP) error {
	f.rows = append(f.rows, *ip)
	return nil
}
func (f *fakeManagedIPsForDomain) Update(ctx context.Context, ip *models.ManagedIP) error {
	for i := range f.rows {
		if f.rows[i].ID == ip.ID {
			f.rows[i] = *ip
		}
	}
	return nil
}
func (f *fakeManagedIPsForDomain) Delete(ctx context.Context, id uint64) error { return nil }
func (f *fakeManagedIPsForDomain) FindByID(ctx context.Context, id uint64) (*models.ManagedIP, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (f *fakeManagedIPsForDomain) FindByAddress(ctx context.Context, addr string) (*models.ManagedIP, error) {
	return nil, repository.ErrNotFound
}
func (f *fakeManagedIPsForDomain) ListAll(ctx context.Context) ([]models.ManagedIP, error) {
	out := make([]models.ManagedIP, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeManagedIPsForDomain) FindUnbound(ctx context.Context) ([]models.ManagedIP, error) {
	return nil, nil
}
func (f *fakeManagedIPsForDomain) CountDomainsUsingIP(ctx context.Context, id uint64) (int64, error) {
	return 0, nil
}
func (f *fakeManagedIPsForDomain) FindDefaultByFamily(ctx context.Context, family string) (*models.ManagedIP, error) {
	for i := range f.rows {
		if f.rows[i].IsDefault && f.rows[i].Family == family {
			cp := f.rows[i]
			return &cp, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeManagedIPsForDomain) EnsureDefault(ctx context.Context, address, family string) error {
	return nil
}

// setupDomainListenIPHarness builds a router with a fake domain repo, a
// fake managed-IP repo, and the requested claims so each test can hit
// PATCH/GET without re-wiring middleware.
func setupDomainListenIPHarness(t *testing.T, claims *auth.AccessClaims, ips *fakeManagedIPsForDomain, dom *models.Domain) (*gin.Engine, *mockDomainRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, claims)
		c.Next()
	})
	repo := newMockDomainRepo()
	if dom != nil {
		repo.domains[dom.ID] = dom
	}
	RegisterDomainRoutes(v1, DomainHandlerConfig{
		Domains:    repo,
		ManagedIPs: ips,
	})
	return r, repo
}

func TestDomainPatch_AdminBindsListenIPv4(t *testing.T) {
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
	}}
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com"}
	r, repo := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "admin", IsAdmin: true}, ips, dom)

	body := bytes.NewBufferString(`{"listen_ipv4_id": 2}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if repo.domains["d1"].ListenIPv4ID == nil || *repo.domains["d1"].ListenIPv4ID != 2 {
		t.Errorf("listen_ipv4_id not persisted; got %+v", repo.domains["d1"].ListenIPv4ID)
	}
	// Response should denormalize the binding.
	var resp struct {
		ListenIPv4 *struct {
			ID      uint64 `json:"id"`
			Address string `json:"address"`
		} `json:"listen_ipv4"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ListenIPv4 == nil || resp.ListenIPv4.ID != 2 || resp.ListenIPv4.Address != "203.0.113.99" {
		t.Errorf("response listen_ipv4 mismatch: %+v", resp.ListenIPv4)
	}
}

func TestDomainPatch_ExplicitNullClearsBinding(t *testing.T) {
	id := uint64(2)
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
	}}
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com", ListenIPv4ID: &id}
	r, repo := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "admin", IsAdmin: true}, ips, dom)

	body := bytes.NewBufferString(`{"listen_ipv4_id": null}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", w.Code, w.Body.String())
	}
	if repo.domains["d1"].ListenIPv4ID != nil {
		t.Errorf("expected nil after explicit null; got %+v", repo.domains["d1"].ListenIPv4ID)
	}
	// Response should fall back to the family default's address.
	var resp struct {
		ListenIPv4 *struct {
			ID      uint64 `json:"id"`
			Address string `json:"address"`
		} `json:"listen_ipv4"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ListenIPv4 == nil || resp.ListenIPv4.ID != 1 || resp.ListenIPv4.Address != "203.0.113.1" {
		t.Errorf("response should fall back to family default; got %+v", resp.ListenIPv4)
	}
}

func TestDomainPatch_FamilyMismatchRejected(t *testing.T) {
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 5, Address: "2001:db8::1", Family: "ipv6"},
	}}
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com"}
	r, _ := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "admin", IsAdmin: true}, ips, dom)

	body := bytes.NewBufferString(`{"listen_ipv4_id": 5}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("family_mismatch")) {
		t.Errorf("body should mention family_mismatch; got %s", w.Body.String())
	}
}

func TestDomainPatch_UserCannotPickNonSelectableIP(t *testing.T) {
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsDefault: true, IsUserSelectable: false},
		{ID: 2, Address: "203.0.113.50", Family: "ipv4", IsUserSelectable: false},
		{ID: 3, Address: "203.0.113.99", Family: "ipv4", IsUserSelectable: true},
	}}
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com"}

	// Try ID 2 (not user-selectable) → 403.
	r, _ := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "u1", IsAdmin: false}, ips, dom)
	body := bytes.NewBufferString(`{"listen_ipv4_id": 2}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin picking non-selectable IP: got %d want 403; body=%s", w.Code, w.Body.String())
	}

	// Try ID 3 (user-selectable) → 200.
	r2, repo := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "u1", IsAdmin: false}, ips, dom)
	body2 := bytes.NewBufferString(`{"listen_ipv4_id": 3}`)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body2)
	req2.Header.Set("Content-Type", "application/json")
	r2.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("non-admin picking user-selectable IP: got %d want 200; body=%s", w2.Code, w2.Body.String())
	}
	if repo.domains["d1"].ListenIPv4ID == nil || *repo.domains["d1"].ListenIPv4ID != 3 {
		t.Errorf("user-selectable IPv4 not persisted; got %+v", repo.domains["d1"].ListenIPv4ID)
	}
}

func TestDomainPatch_AbsentFieldsLeaveBindingsAlone(t *testing.T) {
	id4, id6 := uint64(2), uint64(5)
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
		{ID: 4, Address: "2001:db8::1", Family: "ipv6", IsDefault: true},
		{ID: 5, Address: "2001:db8::99", Family: "ipv6"},
	}}
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com", ListenIPv4ID: &id4, ListenIPv6ID: &id6}
	r, repo := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "admin", IsAdmin: true}, ips, dom)

	// PATCH with only is_enabled — neither IP field should change.
	body := bytes.NewBufferString(`{"is_enabled": false}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d; body=%s", w.Code, w.Body.String())
	}
	if repo.domains["d1"].ListenIPv4ID == nil || *repo.domains["d1"].ListenIPv4ID != 2 {
		t.Errorf("ListenIPv4ID should remain 2; got %+v", repo.domains["d1"].ListenIPv4ID)
	}
	if repo.domains["d1"].ListenIPv6ID == nil || *repo.domains["d1"].ListenIPv6ID != 5 {
		t.Errorf("ListenIPv6ID should remain 5; got %+v", repo.domains["d1"].ListenIPv6ID)
	}
}

func TestDomainGet_DenormalizesListenIPs(t *testing.T) {
	id4 := uint64(2)
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
		{ID: 4, Address: "2001:db8::1", Family: "ipv6", IsDefault: true},
	}}
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com", ListenIPv4ID: &id4}
	r, _ := setupDomainListenIPHarness(t, &auth.AccessClaims{UserID: "admin", IsAdmin: true}, ips, dom)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/d1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ListenIPv4 *ipSummary `json:"listen_ipv4"`
		ListenIPv6 *ipSummary `json:"listen_ipv6"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// IPv4 = explicit binding to id 2.
	if resp.ListenIPv4 == nil || resp.ListenIPv4.ID != 2 || resp.ListenIPv4.Address != "203.0.113.99" {
		t.Errorf("listen_ipv4 mismatch: %+v", resp.ListenIPv4)
	}
	// IPv6 = unset on the domain → falls back to family default.
	if resp.ListenIPv6 == nil || resp.ListenIPv6.ID != 4 || resp.ListenIPv6.Address != "2001:db8::1" {
		t.Errorf("listen_ipv6 fallback mismatch: %+v", resp.ListenIPv6)
	}
}

func TestDomainPatch_PoolUnavailableReturns503(t *testing.T) {
	dom := &models.Domain{ID: "d1", UserID: "u1", Name: "x.com"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "admin", IsAdmin: true})
		c.Next()
	})
	repo := newMockDomainRepo()
	repo.domains[dom.ID] = dom
	RegisterDomainRoutes(v1, DomainHandlerConfig{Domains: repo, ManagedIPs: nil})

	body := bytes.NewBufferString(`{"listen_ipv4_id": 1}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/d1", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want 503; body=%s", w.Code, w.Body.String())
	}
}

func TestUserList_ReturnsOnlySelectable(t *testing.T) {
	// /user/ips endpoint test: fold-in from Step 5 task 4. Only
	// rows with is_user_selectable=true come back, regardless of
	// is_default or family.
	ips := &fakeManagedIPsForDomain{rows: []models.ManagedIP{
		{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsDefault: true, IsUserSelectable: false},
		{ID: 2, Address: "203.0.113.50", Family: "ipv4", IsUserSelectable: true},
		{ID: 3, Address: "2001:db8::1", Family: "ipv6", IsUserSelectable: true},
		{ID: 4, Address: "2001:db8::99", Family: "ipv6", IsUserSelectable: false},
	}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "u1", IsAdmin: false})
		c.Next()
	})
	RegisterIPRoutes(v1, IPHandlerConfig{Repo: ips})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/ips", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d; body=%s", w.Code, w.Body.String())
	}
	var resp ipListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 rows; got %d (%+v)", len(resp.Data), resp.Data)
	}
	for _, r := range resp.Data {
		if !r.IsUserSelectable {
			t.Errorf("unexpected non-selectable row in /user/ips: %+v", r)
		}
	}
}
