package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/dnscompile"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// mtaStsRouter builds a router wired with the same mocks domain_cache /
// dns_test use. Server settings are seeded so publishDNSRecords has the
// PublicIPv4 it needs to emit records.
func mtaStsRouter(t *testing.T, userID string, isAdmin bool) (
	*gin.Engine, *mockDomainRepo, *mockDNSZoneRepo, *mockDNSRecordRepo,
	*mockServerSettingsRepo, *mockDNSReconciler,
) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	if userID != "" {
		v1.Use(func(c *gin.Context) {
			ginctx.SetClaims(c, &auth.AccessClaims{UserID: userID, IsAdmin: isAdmin})
			c.Next()
		})
	}
	dr := newMockDomainRepo()
	zr := newMockDNSZoneRepo()
	rr := newMockDNSRecordRepo()
	ss := &mockServerSettingsRepo{getResult: &models.ServerSettings{
		ID: 1, PublicIPv4: "203.0.113.7", Hostname: "host.example.com",
	}}
	rc := &mockDNSReconciler{}
	RegisterDomainMTAStsRoutes(v1, DomainMTAStsHandlerConfig{
		Domains:        dr,
		DNSZones:       zr,
		DNSRecords:     rr,
		ServerSettings: ss,
		Reconciler:     rc,
	})
	return r, dr, zr, rr, ss, rc
}

func seedMTAStsDomain(t *testing.T, dr *mockDomainRepo, zr *mockDNSZoneRepo) {
	require.NoError(t, dr.Create(context.Background(), &models.Domain{
		ID: "dom1", UserID: "owner", Name: "example.com",
	}))
	require.NoError(t, zr.Create(context.Background(), &models.DNSZone{
		ID: "zone1", DomainID: "dom1", Name: "example.com",
	}))
}

func TestMTASts_OwnerEnables_PublishesDNS_AndSchedules(t *testing.T) {
	r, dr, zr, rr, _, rc := mtaStsRouter(t, "owner", false)
	seedMTAStsDomain(t, dr, zr)

	body, _ := json.Marshal(map[string]any{"enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/mta-sts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["enabled"])
	assert.Equal(t, "policy_published", resp["status_hint"])
	assert.Equal(t, "https://mta-sts.example.com/.well-known/mta-sts.txt", resp["policy_url"])
	// DNS records emitted, both flagged with managed_by="mta-sts".
	recs, _ := rr.ListByZoneID(context.Background(), "zone1")
	require.Len(t, recs, 2)
	for _, r := range recs {
		require.NotNil(t, r.ManagedBy)
		assert.Equal(t, dnscompile.MTAStsRecordsManagedBy, *r.ManagedBy)
	}
	assert.Equal(t, []string{"dom1"}, rc.scheduled)
}

func TestMTASts_DisableRemovesRecords(t *testing.T) {
	r, dr, zr, rr, _, _ := mtaStsRouter(t, "owner", false)
	seedMTAStsDomain(t, dr, zr)
	// enable then disable
	for _, enabled := range []bool{true, false} {
		body, _ := json.Marshal(map[string]any{"enabled": enabled})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/mta-sts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "enabled=%v", enabled)
	}
	recs, _ := rr.ListByZoneID(context.Background(), "zone1")
	assert.Empty(t, recs, "disable should have removed all managed_by=mta-sts records")
}

func TestMTASts_NonOwner_403(t *testing.T) {
	r, dr, zr, _, _, _ := mtaStsRouter(t, "intruder", false)
	seedMTAStsDomain(t, dr, zr)
	body, _ := json.Marshal(map[string]any{"enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/mta-sts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestMTASts_AdminCanToggleAnyDomain(t *testing.T) {
	r, dr, zr, _, _, _ := mtaStsRouter(t, "admin-user", true)
	seedMTAStsDomain(t, dr, zr)
	body, _ := json.Marshal(map[string]any{"enabled": true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/domains/dom1/mta-sts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestMTASts_GetReflectsDBState(t *testing.T) {
	r, dr, zr, _, _, _ := mtaStsRouter(t, "owner", false)
	seedMTAStsDomain(t, dr, zr)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/dom1/mta-sts", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, false, resp["enabled"])
	assert.Equal(t, "off", resp["status_hint"])
}
