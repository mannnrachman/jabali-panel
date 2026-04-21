package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// domainEmailTestRouter returns a gin engine with the domain-email
// routes wired to the supplied mock agent, plus a pre-seeded
// mockDomainRepo containing one domain owned by user1.
func domainEmailTestRouter(t *testing.T, ma *mockAgent, isAdmin bool, userID string) (*gin.Engine, *mockDomainRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{
			UserID:  userID,
			IsAdmin: isAdmin,
		})
		c.Next()
	})

	domains := newMockDomainRepo()
	domains.domains["dom1"] = &models.Domain{
		ID:     "dom1",
		UserID: "user1",
		Name:   "example.com",
	}
	RegisterDomainEmailRoutes(v1, DomainEmailHandlerConfig{
		Domains: domains,
		Agent:   ma,
	})
	return r, domains
}

// TestDomainEmail_Enable_Success is the happy-path. Agent returns a
// valid DKIM response; handler must persist it and return 200 with
// the DNS hint list populated.
func TestDomainEmail_Enable_Success(t *testing.T) {
	ma := &mockAgent{
		callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
			require.Equal(t, "domain.email_enable", cmd)
			return json.RawMessage(`{"ok":true,"dkim_selector":"jabali","dkim_public_key":"v=DKIM1;k=ed25519;p=AAAA"}`), nil
		},
	}
	r, domains := domainEmailTestRouter(t, ma, false, "user1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp domainEmailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.EmailEnabled)
	require.Equal(t, "jabali", resp.DkimSelector)
	require.Equal(t, "v=DKIM1;k=ed25519;p=AAAA", resp.DkimPublicKey)
	require.NotNil(t, resp.EmailEnabledAt)

	// Row state mirrors the response.
	require.True(t, domains.domains["dom1"].EmailEnabled)
	require.NotNil(t, domains.domains["dom1"].DkimSelector)
	require.Equal(t, "jabali", *domains.domains["dom1"].DkimSelector)

	// DNS hints: MX, SPF, DMARC, DKIM — 4 entries post-enable.
	require.Len(t, resp.Records, 4)
}

// TestDomainEmail_Enable_AgentFails verifies the panel does NOT flip
// email_enabled when the agent errors out. The row must stay untouched
// so the operator can retry cleanly.
func TestDomainEmail_Enable_AgentFails(t *testing.T) {
	ma := &mockAgent{callErr: errors.New("boom")}
	r, domains := domainEmailTestRouter(t, ma, false, "user1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.False(t, domains.domains["dom1"].EmailEnabled)
	require.Nil(t, domains.domains["dom1"].DkimSelector)
}

// TestDomainEmail_Enable_AgentBadResponse guards against the agent
// returning ok:false or missing DKIM material — without this check we
// would persist an empty selector and render broken DNS hints.
func TestDomainEmail_Enable_AgentBadResponse(t *testing.T) {
	ma := &mockAgent{
		callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":false}`), nil
		},
	}
	r, domains := domainEmailTestRouter(t, ma, false, "user1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.False(t, domains.domains["dom1"].EmailEnabled)
}

// TestDomainEmail_Enable_ForbiddenOtherOwner — non-admin trying to
// flip another user's domain must hit 403, and the agent must not be
// called. The row stays untouched.
func TestDomainEmail_Enable_ForbiddenOtherOwner(t *testing.T) {
	ma := &mockAgent{}
	r, domains := domainEmailTestRouter(t, ma, false, "user2")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, 0, ma.callCount, "agent must not be contacted on auth failure")
	require.False(t, domains.domains["dom1"].EmailEnabled)
}

// TestDomainEmail_Disable_KeepsDKIM — disable clears email_enabled and
// email_enabled_at but MUST preserve dkim_selector + dkim_public_key
// so a later re-enable doesn't roll the key (ADR-0043).
func TestDomainEmail_Disable_KeepsDKIM(t *testing.T) {
	sel := "jabali"
	pub := "v=DKIM1;k=ed25519;p=AAAA"
	ma := &mockAgent{}
	r, domains := domainEmailTestRouter(t, ma, false, "user1")
	// Preload: already enabled with DKIM material set.
	domains.domains["dom1"].EmailEnabled = true
	domains.domains["dom1"].DkimSelector = &sel
	domains.domains["dom1"].DkimPublicKey = &pub

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.False(t, domains.domains["dom1"].EmailEnabled)
	require.Nil(t, domains.domains["dom1"].EmailEnabledAt)
	// Key material survived the disable — this is the ADR-0043
	// contract; losing it would re-issue a fresh key on next enable
	// and break every cached DKIM receiver until DNS propagates.
	require.NotNil(t, domains.domains["dom1"].DkimSelector)
	require.Equal(t, "jabali", *domains.domains["dom1"].DkimSelector)
	require.NotNil(t, domains.domains["dom1"].DkimPublicKey)
}

// TestDomainEmail_Disable_AgentFails — leave row untouched on agent
// failure so the operator can retry. Matches the mailbox.delete
// ordering pattern.
func TestDomainEmail_Disable_AgentFails(t *testing.T) {
	ma := &mockAgent{callErr: errors.New("boom")}
	r, domains := domainEmailTestRouter(t, ma, false, "user1")
	domains.domains["dom1"].EmailEnabled = true

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.True(t, domains.domains["dom1"].EmailEnabled, "row must stay enabled when agent fails")
}

// TestDomainEmail_Get_ShowsHints — GET on a disabled domain still
// returns the MX/SPF/DMARC hints so the operator can see what they
// will need; the DKIM entry shows the placeholder text.
func TestDomainEmail_Get_ShowsHints(t *testing.T) {
	r, _ := domainEmailTestRouter(t, &mockAgent{}, false, "user1")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domainEmailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.False(t, resp.EmailEnabled)
	require.Len(t, resp.Records, 4) // MX, SPF, DMARC, DKIM-placeholder
	require.Contains(t, resp.Records[3].Value, "", "DKIM placeholder has empty value pre-enable")
}

// TestDomainEmail_Get_NotFound — requesting email for a non-existent
// domain returns 404, not 500.
func TestDomainEmail_Get_NotFound(t *testing.T) {
	r, _ := domainEmailTestRouter(t, &mockAgent{}, false, "user1")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains/unknown/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

