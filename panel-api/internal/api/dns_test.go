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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Mock repositories

type mockDomainRepo struct {
	domains map[string]*models.Domain
}

func newMockDomainRepo() *mockDomainRepo {
	return &mockDomainRepo{domains: make(map[string]*models.Domain)}
}

func (m *mockDomainRepo) Create(ctx context.Context, d *models.Domain) error {
	m.domains[d.ID] = d
	return nil
}

func (m *mockDomainRepo) Update(ctx context.Context, d *models.Domain) error {
	m.domains[d.ID] = d
	return nil
}

func (m *mockDomainRepo) Delete(ctx context.Context, id string) error {
	delete(m.domains, id)
	return nil
}

func (m *mockDomainRepo) FindByID(ctx context.Context, id string) (*models.Domain, error) {
	if d, ok := m.domains[id]; ok {
		return d, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockDomainRepo) FindByName(ctx context.Context, name string) (*models.Domain, error) {
	for _, d := range m.domains {
		if d.Name == name {
			return d, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockDomainRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.Domain, int64, error) {
	return nil, 0, nil
}

func (m *mockDomainRepo) ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.Domain, int64, error) {
	return nil, 0, nil
}

func (m *mockDomainRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	return 0, nil
}

func (m *mockDomainRepo) SetPHPPoolID(ctx context.Context, id string, poolID *string) error {
	return nil
}

func (m *mockDomainRepo) CountByPHPPoolID(ctx context.Context, poolID string) (int64, error) {
	return 0, nil
}

type mockDNSZoneRepo struct {
	zones map[string]*models.DNSZone
}

func newMockDNSZoneRepo() *mockDNSZoneRepo {
	return &mockDNSZoneRepo{zones: make(map[string]*models.DNSZone)}
}

func (m *mockDNSZoneRepo) Create(ctx context.Context, z *models.DNSZone) error {
	m.zones[z.ID] = z
	return nil
}

func (m *mockDNSZoneRepo) Update(ctx context.Context, z *models.DNSZone) error {
	m.zones[z.ID] = z
	return nil
}

func (m *mockDNSZoneRepo) Delete(ctx context.Context, id string) error {
	delete(m.zones, id)
	return nil
}

func (m *mockDNSZoneRepo) FindByID(ctx context.Context, id string) (*models.DNSZone, error) {
	if z, ok := m.zones[id]; ok {
		return z, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockDNSZoneRepo) FindByName(ctx context.Context, name string) (*models.DNSZone, error) {
	for _, z := range m.zones {
		if z.Name == name {
			return z, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockDNSZoneRepo) FindByDomainID(ctx context.Context, domainID string) (*models.DNSZone, error) {
	for _, z := range m.zones {
		if z.DomainID == domainID {
			return z, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockDNSZoneRepo) ListAll(ctx context.Context) ([]models.DNSZone, error) {
	var zones []models.DNSZone
	for _, z := range m.zones {
		zones = append(zones, *z)
	}
	return zones, nil
}

type mockDNSRecordRepo struct {
	records map[string]*models.DNSRecord
}

func newMockDNSRecordRepo() *mockDNSRecordRepo {
	return &mockDNSRecordRepo{records: make(map[string]*models.DNSRecord)}
}

func (m *mockDNSRecordRepo) Create(ctx context.Context, r *models.DNSRecord) error {
	m.records[r.ID] = r
	return nil
}

func (m *mockDNSRecordRepo) Update(ctx context.Context, r *models.DNSRecord) error {
	m.records[r.ID] = r
	return nil
}

func (m *mockDNSRecordRepo) Delete(ctx context.Context, id string) error {
	delete(m.records, id)
	return nil
}

func (m *mockDNSRecordRepo) FindByID(ctx context.Context, id string) (*models.DNSRecord, error) {
	if r, ok := m.records[id]; ok {
		return r, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockDNSRecordRepo) ListByZoneID(ctx context.Context, zoneID string) ([]models.DNSRecord, error) {
	var records []models.DNSRecord
	for _, r := range m.records {
		if r.ZoneID == zoneID {
			records = append(records, *r)
		}
	}
	return records, nil
}

func (m *mockDNSRecordRepo) DeleteByZoneID(ctx context.Context, zoneID string) error {
	for id, r := range m.records {
		if r.ZoneID == zoneID {
			delete(m.records, id)
		}
	}
	return nil
}

// Mock reconciler
type mockDNSReconciler struct {
	scheduled []string
}

func (m *mockDNSReconciler) Schedule(domainID string) {
	m.scheduled = append(m.scheduled, domainID)
}

// Helper to setup router with optional claims
func dnsRouter(userID string, isAdmin bool) (*gin.Engine, *mockDomainRepo, *mockDNSZoneRepo, *mockDNSRecordRepo, *mockDNSReconciler) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")

	// Inject claims if provided
	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{
				UserID:  userID,
				IsAdmin: isAdmin,
			})
			c.Next()
		})
	}

	domainRepo := newMockDomainRepo()
	zoneRepo := newMockDNSZoneRepo()
	recordRepo := newMockDNSRecordRepo()
	reconciler := &mockDNSReconciler{}

	RegisterDNSRoutes(v1, DNSHandlerConfig{
		Domains:    domainRepo,
		Zones:      zoneRepo,
		Records:    recordRepo,
		Reconciler: reconciler,
	})

	return r, domainRepo, zoneRepo, recordRepo, reconciler
}

// Test cases

func TestGetZoneUnauthenticated(t *testing.T) {
	r, _, _, _, _ := dnsRouter("", false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/test-domain-id/dns/zone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "unauthenticated", resp["error"])
}

func TestGetZoneNotFound(t *testing.T) {
	r, domainRepo, _, _, _ := dnsRouter("user1", false)

	// Add domain to repo
	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/test-domain-id/dns/zone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "zone_not_provisioned", resp["error"])
}

func TestGetZoneNonOwnerForbidden(t *testing.T) {
	r, domainRepo, _, _, _ := dnsRouter("user2", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/test-domain-id/dns/zone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetZoneSuccess(t *testing.T) {
	r, domainRepo, zoneRepo, _, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/test-domain-id/dns/zone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp["zone"])
}

func TestUpdateZoneSOATimers(t *testing.T) {
	r, domainRepo, zoneRepo, _, reconciler := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Update with new refresh_seconds
	refreshSeconds := 7200
	body, _ := json.Marshal(map[string]interface{}{
		"refresh_seconds": refreshSeconds,
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/test-domain-id/dns/zone", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify reconciler was called
	assert.Equal(t, 1, len(reconciler.scheduled))
	assert.Equal(t, "test-domain-id", reconciler.scheduled[0])

	// Verify zone was updated
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	zoneResp := resp["zone"].(map[string]interface{})
	assert.Equal(t, float64(7200), zoneResp["refresh_seconds"])
}

func TestUpdateZoneOutOfRangeRefresh(t *testing.T) {
	r, domainRepo, zoneRepo, _, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Update with invalid refresh_seconds (too low)
	body, _ := json.Marshal(map[string]interface{}{
		"refresh_seconds": 30,
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/test-domain-id/dns/zone", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "validation_failed", resp["error"])
}

func TestCreateRecordSuccess(t *testing.T) {
	r, domainRepo, zoneRepo, _, reconciler := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Create A record
	body, _ := json.Marshal(map[string]interface{}{
		"name":    "www",
		"type":    "A",
		"content": "192.168.1.1",
		"ttl":     3600,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/test-domain-id/dns/records", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	// Verify reconciler was called
	assert.Equal(t, 1, len(reconciler.scheduled))
	assert.Equal(t, "test-domain-id", reconciler.scheduled[0])

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp["record"])
}

func TestCreateRecordInvalidIPv4(t *testing.T) {
	r, domainRepo, zoneRepo, _, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Create A record with IPv6 content
	body, _ := json.Marshal(map[string]interface{}{
		"name":    "www",
		"type":    "A",
		"content": "::1",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/test-domain-id/dns/records", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "invalid_record", resp["error"])
}

func TestCreateRecordLongTXT(t *testing.T) {
	r, domainRepo, zoneRepo, _, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Create TXT record with overly long content
	longContent := ""
	for i := 0; i < 5000; i++ {
		longContent += "a"
	}

	body, _ := json.Marshal(map[string]interface{}{
		"name":    "_dmarc",
		"type":    "TXT",
		"content": longContent,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/test-domain-id/dns/records", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "invalid_record", resp["error"])
}

func TestUpdateRecordManagedSOAForbidden(t *testing.T) {
	r, domainRepo, zoneRepo, recordRepo, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	recordID := ids.NewULID()
	record := &models.DNSRecord{
		ID:        recordID,
		ZoneID:    zoneID,
		Name:      "@",
		Type:      "SOA",
		Content:   "ns.example.com. admin.example.com. 1 3600 600 604800 3600",
		TTL:       3600,
		Priority:  0,
		Managed:   true,
		IsEnabled: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	recordRepo.Create(context.Background(), record)

	// Try to update managed SOA
	body, _ := json.Marshal(map[string]interface{}{
		"content": "updated.example.com. admin.example.com. 2 3600 600 604800 3600",
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dns/records/"+recordID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "record_managed", resp["error"])
}

func TestUpdateRecordManagedARecord(t *testing.T) {
	r, domainRepo, zoneRepo, recordRepo, reconciler := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	recordID := ids.NewULID()
	record := &models.DNSRecord{
		ID:        recordID,
		ZoneID:    zoneID,
		Name:      "@",
		Type:      "A",
		Content:   "192.168.1.1",
		TTL:       3600,
		Priority:  0,
		Managed:   true,
		IsEnabled: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	recordRepo.Create(context.Background(), record)

	// Update managed A record (should succeed - it's editable)
	body, _ := json.Marshal(map[string]interface{}{
		"content": "192.168.1.2",
	})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dns/records/"+recordID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Verify reconciler was called
	assert.Equal(t, 1, len(reconciler.scheduled))

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	recordResp := resp["record"].(map[string]interface{})
	assert.Equal(t, "192.168.1.2", recordResp["content"])
}

func TestDeleteRecordManagedNSForbidden(t *testing.T) {
	r, domainRepo, zoneRepo, recordRepo, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	recordID := ids.NewULID()
	record := &models.DNSRecord{
		ID:        recordID,
		ZoneID:    zoneID,
		Name:      "@",
		Type:      "NS",
		Content:   "ns1.example.com.",
		TTL:       3600,
		Priority:  0,
		Managed:   true,
		IsEnabled: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	recordRepo.Create(context.Background(), record)

	// Try to delete managed NS
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dns/records/"+recordID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "record_managed", resp["error"])
}

func TestDeleteRecordNonManagedSuccess(t *testing.T) {
	r, domainRepo, zoneRepo, recordRepo, reconciler := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	recordID := ids.NewULID()
	record := &models.DNSRecord{
		ID:        recordID,
		ZoneID:    zoneID,
		Name:      "www",
		Type:      "A",
		Content:   "192.168.1.1",
		TTL:       3600,
		Priority:  0,
		Managed:   false,
		IsEnabled: true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	recordRepo.Create(context.Background(), record)

	// Delete non-managed record
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dns/records/"+recordID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)

	// Verify reconciler was called
	assert.Equal(t, 1, len(reconciler.scheduled))
	assert.Equal(t, "test-domain-id", reconciler.scheduled[0])
}

func TestListRecordsSuccess(t *testing.T) {
	r, domainRepo, zoneRepo, recordRepo, _ := dnsRouter("user1", false)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Add records
	for i := 0; i < 3; i++ {
		record := &models.DNSRecord{
			ID:        ids.NewULID(),
			ZoneID:    zoneID,
			Name:      "www",
			Type:      "A",
			Content:   "192.168.1.1",
			TTL:       3600,
			Priority:  0,
			Managed:   false,
			IsEnabled: true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		recordRepo.Create(context.Background(), record)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/test-domain-id/dns/records", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	records := resp["records"].([]interface{})
	assert.Equal(t, 3, len(records))
}

func TestAdminCanAccessAnyDomain(t *testing.T) {
	r, domainRepo, zoneRepo, _, _ := dnsRouter("admin-user", true)

	domain := &models.Domain{
		ID:     "test-domain-id",
		UserID: "user1",
		Name:   "example.com",
	}
	domainRepo.Create(context.Background(), domain)

	zoneID := ids.NewULID()
	zone := &models.DNSZone{
		ID:             zoneID,
		DomainID:       "test-domain-id",
		Name:           "example.com",
		Serial:         1,
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	zoneRepo.Create(context.Background(), zone)

	// Admin accessing user1's domain zone
	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/test-domain-id/dns/zone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp["zone"])
}
