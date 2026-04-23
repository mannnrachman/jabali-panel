// mailbox_autoresponder.go — M6.5 Step 3 autoresponder HTTP handlers.
//
// Wire contract: GET/PUT/DELETE /mailboxes/:mbid/autoresponder
// Backed by JMAP VacationResponse (RFC 8621 §8) via the reconciler
// phase + agent autoresponder.set command.

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// MailboxAutoresponderHandlerConfig wires the repos + agent this handler needs.
type MailboxAutoresponderHandlerConfig struct {
	Mailboxes      repository.MailboxRepository
	Domains        repository.DomainRepository
	Autoresponders repository.EmailAutoresponderRepository
	Agent          agent.AgentInterface
}

// autoresponderResponse is the JSON envelope the panel UI consumes.
type autoresponderResponse struct {
	MailboxID string     `json:"mailbox_id"`
	Enabled   bool       `json:"enabled"`
	FromDate  *time.Time `json:"from_date"`
	ToDate    *time.Time `json:"to_date"`
	Subject   *string    `json:"subject"`
	TextBody  *string    `json:"text_body"`
	HTMLBody  *string    `json:"html_body"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type autoresponderUpdateRequest struct {
	Enabled  bool       `json:"enabled"`
	FromDate *time.Time `json:"from_date"`
	ToDate   *time.Time `json:"to_date"`
	Subject  *string    `json:"subject"`
	TextBody *string    `json:"text_body"`
	HTMLBody *string    `json:"html_body"`
}

type mailboxAutoresponderHandler struct {
	cfg MailboxAutoresponderHandlerConfig
}

// RegisterMailboxAutoresponderRoutes mounts the endpoints under g.
// Called from routes_m65.go's registerAutoresponderRoutes.
func RegisterMailboxAutoresponderRoutes(g *gin.RouterGroup, cfg MailboxAutoresponderHandlerConfig) {
	if cfg.Autoresponders == nil {
		return
	}
	h := &mailboxAutoresponderHandler{cfg: cfg}
	g.GET("/mailboxes/:mbid/autoresponder", h.get)
	g.PUT("/mailboxes/:mbid/autoresponder", h.put)
	g.DELETE("/mailboxes/:mbid/autoresponder", h.del)
}

func (h *mailboxAutoresponderHandler) loadMailboxWithAuth(ctx context.Context, id string, claims *auth.AccessClaims) (*models.Mailbox, *models.Domain, error) {
	mb, err := h.cfg.Mailboxes.FindByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	dom, err := h.cfg.Domains.FindByID(ctx, mb.DomainID)
	if err != nil {
		return nil, nil, err
	}
	if !claims.IsAdmin && dom.UserID != claims.UserID {
		return nil, nil, errMailboxForbidden
	}
	return mb, dom, nil
}

func (h *mailboxAutoresponderHandler) get(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	mb, _, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		// Reuse mailbox handler's writeLoadErr via generic mapping.
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if errors.Is(err, errMailboxForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	ar, err := h.cfg.Autoresponders.FindByMailboxID(ctx, mb.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Not set — return empty shape with defaults.
			c.JSON(http.StatusOK, autoresponderResponse{
				MailboxID: mb.ID,
				Enabled:   false,
				UpdatedAt: time.Time{},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, autoresponderResponse{
		MailboxID: ar.MailboxID,
		Enabled:   ar.Enabled,
		FromDate:  ar.FromDate,
		ToDate:    ar.ToDate,
		Subject:   ar.Subject,
		TextBody:  ar.TextBody,
		HTMLBody:  ar.HTMLBody,
		UpdatedAt: ar.UpdatedAt,
	})
}

func (h *mailboxAutoresponderHandler) put(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	mb, _, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if errors.Is(err, errMailboxForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	var req autoresponderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}

	ar := &models.EmailAutoresponder{
		MailboxID: mb.ID,
		Enabled:   req.Enabled,
		FromDate:  req.FromDate,
		ToDate:    req.ToDate,
		Subject:   req.Subject,
		TextBody:  req.TextBody,
		HTMLBody:  req.HTMLBody,
		ManagedBy: "m6.5",
	}
	if err := h.cfg.Autoresponders.Update(ctx, ar); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Best-effort inline push to Stalwart. Reconciler re-asserts on drift.
	if h.cfg.Agent != nil {
		email := mb.LocalPart + "@" + mustDomainName(ctx, h.cfg.Domains, mb.DomainID)
		params := map[string]any{
			"mailbox_email": email,
			"enabled":       req.Enabled,
		}
		if req.FromDate != nil {
			params["from_date"] = req.FromDate.UTC().Format(time.RFC3339)
		}
		if req.ToDate != nil {
			params["to_date"] = req.ToDate.UTC().Format(time.RFC3339)
		}
		if req.Subject != nil {
			params["subject"] = *req.Subject
		}
		if req.TextBody != nil {
			params["text_body"] = *req.TextBody
		}
		if req.HTMLBody != nil {
			params["html_body"] = *req.HTMLBody
		}
		agentCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, _ = h.cfg.Agent.Call(agentCtx, "autoresponder.set", params)
	}

	c.JSON(http.StatusOK, autoresponderResponse{
		MailboxID: ar.MailboxID,
		Enabled:   ar.Enabled,
		FromDate:  ar.FromDate,
		ToDate:    ar.ToDate,
		Subject:   ar.Subject,
		TextBody:  ar.TextBody,
		HTMLBody:  ar.HTMLBody,
		UpdatedAt: ar.UpdatedAt,
	})
}

func (h *mailboxAutoresponderHandler) del(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	mb, _, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		if errors.Is(err, errMailboxForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if err := h.cfg.Autoresponders.Delete(ctx, mb.ID); err != nil && !errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	// Disable on Stalwart too.
	if h.cfg.Agent != nil {
		email := mb.LocalPart + "@" + mustDomainName(ctx, h.cfg.Domains, mb.DomainID)
		agentCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_, _ = h.cfg.Agent.Call(agentCtx, "autoresponder.set", map[string]any{
			"mailbox_email": email,
			"enabled":       false,
		})
	}
	c.JSON(http.StatusNoContent, nil)
}

// mustDomainName resolves a domain name by id; returns "" on error.
// Callers are best-effort pushes — Stalwart rejects empty domain names.
func mustDomainName(ctx context.Context, domains repository.DomainRepository, domainID string) string {
	if domains == nil {
		return ""
	}
	d, err := domains.FindByID(ctx, domainID)
	if err != nil || d == nil {
		return ""
	}
	return d.Name
}
