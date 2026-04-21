package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainEmailHandlerConfig wires the email-on-domain endpoints.
type DomainEmailHandlerConfig struct {
	Domains repository.DomainRepository
	Agent   agent.AgentInterface
}

const (
	// domainEmailAgentTimeout bounds the agent call budget for
	// email_enable/disable. These talk to Stalwart over the panel's
	// admin loopback and generate an Ed25519 DKIM keypair on enable —
	// both fast. 30s is generous.
	domainEmailAgentTimeout = 30 * time.Second
)

// RegisterDomainEmailRoutes mounts:
//
//   - GET    /domains/:id/email      current state (enabled flag, DKIM, recommended DNS)
//   - POST   /domains/:id/email      enable (idempotent — re-enables are ok)
//   - DELETE /domains/:id/email      disable (keeps DKIM key material per ADR-0043)
//
// Live DNS-record presence status (the blueprint's /email/dns-status)
// depends on M6 Step 5 (DNS autoconfig) which hasn't landed yet. Until
// then, GET returns a static list of the records the operator should
// publish (hint-only). The UI renders that as static instructions; once
// Step 5 ships, the "status" subfield can be populated without breaking
// the response shape.
func RegisterDomainEmailRoutes(g *gin.RouterGroup, cfg DomainEmailHandlerConfig) {
	h := &domainEmailHandler{cfg: cfg}
	g.GET("/domains/:id/email", h.get)
	g.POST("/domains/:id/email", h.enable)
	g.DELETE("/domains/:id/email", h.disable)
}

type domainEmailHandler struct{ cfg DomainEmailHandlerConfig }

// domainEmailResponse is what the UI reads on every poll. Keep the
// shape stable — dns_records will grow a per-record `status` field
// once Step 5 lands; clients that only look at `records` today will
// continue to render them as static instructions.
type domainEmailResponse struct {
	DomainID       string                `json:"domain_id"`
	DomainName     string                `json:"domain_name"`
	EmailEnabled   bool                  `json:"email_enabled"`
	DkimSelector   string                `json:"dkim_selector,omitempty"`
	DkimPublicKey  string                `json:"dkim_public_key,omitempty"`
	EmailEnabledAt *time.Time            `json:"email_enabled_at,omitempty"`
	Records        []domainEmailDNSHint  `json:"records"`
}

// domainEmailDNSHint is one recommended DNS record. `Status` is an
// empty string today (no live status) and becomes "ok" / "missing" /
// "conflict" once the dns-status endpoint is wired. `Purpose` is a
// human label for the UI table.
type domainEmailDNSHint struct {
	Purpose string `json:"purpose"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Status  string `json:"status,omitempty"`
}

func (h *domainEmailHandler) get(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	dom, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	selector, pubKey := "", ""
	if dom.DkimSelector != nil {
		selector = *dom.DkimSelector
	}
	if dom.DkimPublicKey != nil {
		pubKey = *dom.DkimPublicKey
	}
	c.JSON(http.StatusOK, domainEmailResponse{
		DomainID:       dom.ID,
		DomainName:     dom.Name,
		EmailEnabled:   dom.EmailEnabled,
		DkimSelector:   selector,
		DkimPublicKey:  pubKey,
		EmailEnabledAt: dom.EmailEnabledAt,
		Records:        dnsRecordHints(dom.Name, selector, pubKey),
	})
}

func (h *domainEmailHandler) enable(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	dom, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Agent generates the DKIM keypair + registers the domain in
	// Stalwart; response carries selector + public key so we can
	// mirror it back to DNS. Idempotent on the agent side — calling
	// it twice just re-reads the on-disk key.
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "agent_unconfigured"})
		return
	}
	agentCtx, cancel := context.WithTimeout(ctx, domainEmailAgentTimeout)
	defer cancel()
	raw, err := h.cfg.Agent.Call(agentCtx, "domain.email_enable", map[string]any{
		"domain_id":   dom.ID,
		"domain_name": dom.Name,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	var resp struct {
		Ok            bool   `json:"ok"`
		DKIMSelector  string `json:"dkim_selector"`
		DKIMPublicKey string `json:"dkim_public_key"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_bad_response", "detail": err.Error()})
		return
	}
	if !resp.Ok || resp.DKIMSelector == "" || resp.DKIMPublicKey == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_bad_response"})
		return
	}

	now := time.Now().UTC()
	selector, pubKey := resp.DKIMSelector, resp.DKIMPublicKey
	if err := h.cfg.Domains.UpdateEmailState(ctx, dom.ID, repository.DomainEmailState{
		Enabled:        true,
		DkimSelector:   &selector,
		DkimPublicKey:  &pubKey,
		EmailEnabledAt: &now,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	dom.EmailEnabled = true
	dom.DkimSelector = &selector
	dom.DkimPublicKey = &pubKey
	dom.EmailEnabledAt = &now
	c.JSON(http.StatusOK, domainEmailResponse{
		DomainID:       dom.ID,
		DomainName:     dom.Name,
		EmailEnabled:   true,
		DkimSelector:   selector,
		DkimPublicKey:  pubKey,
		EmailEnabledAt: &now,
		Records:        dnsRecordHints(dom.Name, selector, pubKey),
	})
}

func (h *domainEmailHandler) disable(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	dom, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Agent is authoritative for the Stalwart-side teardown; if it
	// fails we leave the DB row alone (email_enabled=1 still) so the
	// operator can retry. This matches the delete-ordering pattern in
	// mailbox.delete.
	if h.cfg.Agent == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "agent_unconfigured"})
		return
	}
	agentCtx, cancel := context.WithTimeout(ctx, domainEmailAgentTimeout)
	defer cancel()
	if _, err := h.cfg.Agent.Call(agentCtx, "domain.email_disable", map[string]any{
		"domain_id":   dom.ID,
		"domain_name": dom.Name,
	}); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	if err := h.cfg.Domains.UpdateEmailState(ctx, dom.ID, repository.DomainEmailState{
		Enabled:        false,
		EmailEnabledAt: nil,
		// Deliberately pass nil selector/pubkey — disable keeps the
		// DKIM material so re-enable doesn't re-roll the key.
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.Status(http.StatusNoContent)
}

// dnsRecordHints returns the canonical set of records that should be
// published in the domain's zone for email to work. Pure function of
// (domain name, DKIM selector, DKIM public-key TXT value). When email
// is disabled (selector/pubkey empty) the DKIM entry still appears so
// the UI can show the user *what* they'll need when they enable.
func dnsRecordHints(name, selector, pubKey string) []domainEmailDNSHint {
	hints := []domainEmailDNSHint{
		{
			Purpose: "MX — delivers incoming mail to this host",
			Name:    name + ".",
			Type:    "MX",
			Value:   "10 mail." + name + ".",
		},
		{
			Purpose: "SPF — authorises this host to send mail for the domain",
			Name:    name + ".",
			Type:    "TXT",
			Value:   "v=spf1 mx -all",
		},
		{
			Purpose: "DMARC — tells receivers to reject unauthenticated mail",
			Name:    "_dmarc." + name + ".",
			Type:    "TXT",
			Value:   "v=DMARC1; p=reject; rua=mailto:postmaster@" + name,
		},
	}
	if selector != "" && pubKey != "" {
		hints = append(hints, domainEmailDNSHint{
			Purpose: "DKIM — signs outbound mail so receivers can verify it",
			Name:    selector + "._domainkey." + name + ".",
			Type:    "TXT",
			Value:   pubKey,
		})
	} else {
		hints = append(hints, domainEmailDNSHint{
			Purpose: "DKIM — generated automatically when email is enabled",
			Name:    "<selector>._domainkey." + name + ".",
			Type:    "TXT",
			Value:   "",
		})
	}
	return hints
}
