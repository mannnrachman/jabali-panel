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
// mockDomainRepo containing one domain owned by user1. DNS repos are
// nil — exercise the pre-Step-6 no-DNS path.
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

// domainEmailRouterWithDNS wires domain + zone + record mocks together
// and seeds the zone row the handler will look up on enable/disable.
func domainEmailRouterWithDNS(t *testing.T, ma *mockAgent) (*gin.Engine, *mockDomainRepo, *mockDNSZoneRepo, *mockDNSRecordRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(func(c *gin.Context) {
		ginctx.SetClaims(c, &auth.AccessClaims{UserID: "user1", IsAdmin: false})
		c.Next()
	})

	domains := newMockDomainRepo()
	domains.domains["dom1"] = &models.Domain{
		ID:     "dom1",
		UserID: "user1",
		Name:   "example.com",
	}
	zones := newMockDNSZoneRepo()
	zones.zones["zone1"] = &models.DNSZone{ID: "zone1", DomainID: "dom1", Name: "example.com"}
	records := newMockDNSRecordRepo()

	RegisterDomainEmailRoutes(v1, DomainEmailHandlerConfig{
		Domains:    domains,
		Agent:      ma,
		DNSZones:   zones,
		DNSRecords: records,
	})
	return r, domains, zones, records
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
	// Six records in the hint list: MX, SPF, DMARC, autoconfig CNAME,
	// _autodiscover._tcp SRV, DKIM. Expanded from the pre-Step-6 count
	// of 4 when we added autoconfig + autodiscover as part of the DNS
	// autoconfig work.
	require.Len(t, resp.Records, 6)
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
	// Six records in the hint list: MX, SPF, DMARC, autoconfig CNAME,
	// _autodiscover._tcp SRV, DKIM. Expanded from the pre-Step-6 count
	// of 4 when we added autoconfig + autodiscover as part of the DNS
	// autoconfig work.
	require.Len(t, resp.Records, 6)
	// Last record is the DKIM placeholder before enable — empty Value
	// is the contract for "generated later". Index 5 (last) is the
	// DKIM slot; indices 3 + 4 are autoconfig/autodiscover.
	require.Equal(t, "TXT", resp.Records[5].Type)
	require.Contains(t, resp.Records[5].Purpose, "DKIM")
	require.Equal(t, "", resp.Records[5].Value, "DKIM placeholder has empty value pre-enable")
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

// ---- M6 Step 6: DNS autoconfig sync ---------------------------------

// Enable with DNS repos wired must insert the three M6 records
// (DKIM + autoconfig CNAME + _autodiscover._tcp SRV) into the zone,
// each stamped with ManagedBy="m6" so disable can scope its cleanup.
func TestDomainEmail_Enable_InsertsM6DNSRecords(t *testing.T) {
	ma := &mockAgent{
		callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true,"dkim_selector":"jabali","dkim_public_key":"v=DKIM1;k=ed25519;p=AAAA"}`), nil
		},
	}
	r, _, _, records := domainEmailRouterWithDNS(t, ma)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Collect just the M6-managed rows — the inserted delta. Every row
	// with ManagedBy=="m6" must be one of the three expected names.
	got := map[string]string{}
	for _, rec := range records.records {
		if rec.ManagedBy == nil || *rec.ManagedBy != "m6" {
			continue
		}
		got[rec.Type+":"+rec.Name] = rec.Content
	}
	require.Len(t, got, 3, "expected 3 M6 records, got %v", got)
	require.Equal(t, `"v=DKIM1;k=ed25519;p=AAAA"`, got["TXT:jabali._domainkey"])
	require.Equal(t, "mail", got["CNAME:autoconfig"])
	require.Equal(t, "0 0 443 mail", got["SRV:_autodiscover._tcp"])
}

// Disable must remove every ManagedBy="m6" row — and nothing else. We
// seed the zone with one M4 bootstrap row (ManagedBy=nil) + one
// user-edited row (ManagedBy=nil, Managed=false) to prove the scoped
// delete leaves them both intact. Keeping user edits is the whole
// point of the managed_by column (ADR-0042 style: explicit ownership).
func TestDomainEmail_Disable_DeletesOnlyM6Records(t *testing.T) {
	ma := &mockAgent{}
	r, domains, _, records := domainEmailRouterWithDNS(t, ma)

	// Mark the domain as enabled with DKIM set so disable has work.
	sel := "jabali"
	pub := "v=DKIM1;k=ed25519;p=AAAA"
	domains.domains["dom1"].EmailEnabled = true
	domains.domains["dom1"].DkimSelector = &sel
	domains.domains["dom1"].DkimPublicKey = &pub

	// Seed: 1 M6 row (will be deleted), 1 M4 row (must survive),
	// 1 user-edited row (must survive).
	m6 := "m6"
	records.records["r-m6-dkim"] = &models.DNSRecord{
		ID: "r-m6-dkim", ZoneID: "zone1",
		Name: "jabali._domainkey", Type: "TXT", Content: `"v=DKIM1..."`,
		Managed: true, ManagedBy: &m6,
	}
	records.records["r-m4-mx"] = &models.DNSRecord{
		ID: "r-m4-mx", ZoneID: "zone1",
		Name: "@", Type: "MX", Content: "mail", Priority: 10,
		Managed: true, ManagedBy: nil,
	}
	records.records["r-user-txt"] = &models.DNSRecord{
		ID: "r-user-txt", ZoneID: "zone1",
		Name: "@", Type: "TXT", Content: `"custom spf rule"`,
		Managed: false, ManagedBy: nil,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	// The M6 row is gone; both non-M6 rows survive — this is the
	// contract that keeps user overrides alive across an
	// enable/disable cycle.
	_, m6Present := records.records["r-m6-dkim"]
	require.False(t, m6Present, "M6 row should be deleted")
	require.Contains(t, records.records, "r-m4-mx", "M4 bootstrap row must survive")
	require.Contains(t, records.records, "r-user-txt", "user-edited row must survive")
}

// Enable against a zone that already has a user-edited row at (autoconfig,
// CNAME) must skip that specific insert and surface a warning in the
// response — never overwrite the user's choice.
func TestDomainEmail_Enable_UserOverride_Warns(t *testing.T) {
	ma := &mockAgent{
		callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true,"dkim_selector":"jabali","dkim_public_key":"v=DKIM1;k=ed25519;p=AAAA"}`), nil
		},
	}
	r, _, _, records := domainEmailRouterWithDNS(t, ma)
	// Pre-existing user-edited CNAME at the autoconfig slot.
	records.records["r-user-autoconfig"] = &models.DNSRecord{
		ID: "r-user-autoconfig", ZoneID: "zone1",
		Name: "autoconfig", Type: "CNAME", Content: "something-else",
		Managed: false, ManagedBy: nil,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domainEmailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	// DKIM + autodiscover got added; autoconfig was blocked by the
	// user-edited row, so only 2 M6-managed rows in the store.
	m6Count := 0
	for _, r := range records.records {
		if r.ManagedBy != nil && *r.ManagedBy == "m6" {
			m6Count++
		}
	}
	require.Equal(t, 2, m6Count, "user-edited autoconfig row blocks exactly one insert")

	// Warning must mention autoconfig so the operator knows what to
	// fix; the exact wording isn't locked down to avoid brittleness.
	hasAutoconfigWarn := false
	for _, w := range resp.Warnings {
		if contains(w, "autoconfig") {
			hasAutoconfigWarn = true
			break
		}
	}
	require.True(t, hasAutoconfigWarn, "warnings should mention the autoconfig conflict; got %v", resp.Warnings)
}

// Enable twice is idempotent — the second POST must not create
// duplicate M6 rows. Same row count before and after the second call.
func TestDomainEmail_Enable_Idempotent(t *testing.T) {
	ma := &mockAgent{
		callFn: func(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true,"dkim_selector":"jabali","dkim_public_key":"v=DKIM1;k=ed25519;p=AAAA"}`), nil
		},
	}
	r, _, _, records := domainEmailRouterWithDNS(t, ma)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/domains/dom1/email", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "enable call %d", i+1)
	}

	m6Count := 0
	for _, r := range records.records {
		if r.ManagedBy != nil && *r.ManagedBy == "m6" {
			m6Count++
		}
	}
	require.Equal(t, 3, m6Count, "re-enable must not duplicate rows")
}


