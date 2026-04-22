package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// settingsEmailRouter builds a minimal router with admin claims injected
// and the settings/email routes mounted against a shared mockDomainRepo.
func settingsEmailRouter(t *testing.T, repo *mockDomainRepo) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "admin1", IsAdmin: true})
		c.Next()
	})
	RegisterSettingsEmailRoutes(v1, SettingsEmailHandlerConfig{
		Domains: repo,
		Log:     slog.Default(),
	})
	return r
}

// TestSettingsEmail_ReturnsPrimaryDomain: panel-primary row exists with DKIM
// converged — 200 with the full shape.
func TestSettingsEmail_ReturnsPrimaryDomain(t *testing.T) {
	t.Parallel()

	repo := newMockDomainRepo()
	now := time.Date(2026, 4, 22, 18, 0, 0, 0, time.UTC)
	pk := "p=MIIBIjANBg... (trimmed)"
	repo.domains["dom_panel"] = &models.Domain{
		ID:             "dom_panel",
		Name:           "jabali-panel.local",
		IsPanelPrimary: true,
		EmailEnabled:   true,
		DkimPublicKey:  &pk,
		EmailEnabledAt: &now,
	}

	r := settingsEmailRouter(t, repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body settingsEmailOK
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "jabali-panel.local", body.PrimaryDomainName)
	assert.Equal(t, "https://mail.jabali-panel.local/", body.WebmailURL)
	assert.True(t, body.DKIMPublished)
	require.NotNil(t, body.EmailEnabledAt)
	assert.Equal(t, now.Unix(), body.EmailEnabledAt.Unix())
}

// TestSettingsEmail_DKIMNotPublished: row exists but DKIM not yet converged
// (nil public key). 200 with dkim_published=false. Clients show "Initializing"
// badge even though the row is present.
func TestSettingsEmail_DKIMNotPublished(t *testing.T) {
	t.Parallel()

	repo := newMockDomainRepo()
	repo.domains["dom_panel"] = &models.Domain{
		ID:             "dom_panel",
		Name:           "jabali-panel.local",
		IsPanelPrimary: true,
		EmailEnabled:   true,
		DkimPublicKey:  nil,
	}

	r := settingsEmailRouter(t, repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body settingsEmailOK
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.False(t, body.DKIMPublished)
}

// TestSettingsEmail_Absent_Returns202: no panel-primary row → 202 with
// minimal "initializing" shape. Critical wire-contract test.
func TestSettingsEmail_Absent_Returns202(t *testing.T) {
	t.Parallel()

	repo := newMockDomainRepo()

	r := settingsEmailRouter(t, repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Nil(t, body["primary_domain_name"])
	assert.Equal(t, "initializing", body["status"])
	// The 202 body must NOT contain the 200 fields — clients switch on
	// status code and our struct choice enforces this at encode time.
	_, hasURL := body["webmail_url"]
	_, hasDKIM := body["dkim_published"]
	_, hasTS := body["email_enabled_at"]
	assert.False(t, hasURL, "webmail_url must not appear in 202 body")
	assert.False(t, hasDKIM, "dkim_published must not appear in 202 body")
	assert.False(t, hasTS, "email_enabled_at must not appear in 202 body")
}
