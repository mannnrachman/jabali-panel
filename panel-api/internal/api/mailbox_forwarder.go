// mailbox_forwarder.go — M6.5 Step 5 forwarders (alias + external).
//
// Wire contract:
//   GET    /mail/forwarders                    all forwarders for caller's mailboxes
//   POST   /mailboxes/:mbid/forwarders         create forwarder (alias or external)
//   DELETE /forwarders/:id                     delete
//
// Type=alias → x:EmailAlias on UserAccount.aliases.
// Type=external → entry in jabali-fwds sieve script (concatenated per mailbox).

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type MailboxForwarderHandlerConfig struct {
	Mailboxes  repository.MailboxRepository
	Domains    repository.DomainRepository
	Forwarders repository.EmailForwarderRepository
	Agent      agent.AgentInterface
}

type forwarderResponse struct {
	ID           string `json:"id"`
	MailboxID    string `json:"mailbox_id"`
	MailboxEmail string `json:"mailbox_email"`
	DomainID     string `json:"domain_id"`
	DomainName   string `json:"domain_name"`
	Type         string `json:"type"`
	LocalPart    string `json:"local_part,omitempty"`
	Target       string `json:"target"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at"`
}

type forwarderCreateRequest struct {
	Type      string `json:"type"`       // alias | external
	LocalPart string `json:"local_part"` // for alias
	Target    string `json:"target"`     // for external (email); for alias (mailbox@domain)
}

type forwarderHandler struct {
	cfg MailboxForwarderHandlerConfig
}

func RegisterMailboxForwarderRoutes(g *gin.RouterGroup, cfg MailboxForwarderHandlerConfig) {
	if cfg.Forwarders == nil {
		return
	}
	h := &forwarderHandler{cfg: cfg}
	g.GET("/mail/forwarders", h.listAll)
	g.POST("/mailboxes/:mbid/forwarders", h.create)
	g.DELETE("/forwarders/:id", h.del)
}

func (h *forwarderHandler) loadMailbox(ctx context.Context, id string, claims *auth.AccessClaims) (*models.Mailbox, *models.Domain, error) {
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

func (h *forwarderHandler) listAll(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	fwds, _, err := h.cfg.Forwarders.ListAll(ctx, repository.ListOptions{Limit: 500})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	items := make([]forwarderResponse, 0, len(fwds))
	for _, f := range fwds {
		if f.MailboxID == nil {
			// Domain-scoped DA-imported forwarder; M65 mailbox-keyed UI
			// can't render it. Skip — future domain-scoped endpoint will
			// surface these.
			continue
		}
		mb, err := h.cfg.Mailboxes.FindByID(ctx, *f.MailboxID)
		if err != nil {
			continue
		}
		dom, err := h.cfg.Domains.FindByID(ctx, f.DomainID)
		if err != nil {
			continue
		}
		if !claims.IsAdmin && dom.UserID != claims.UserID {
			continue
		}
		items = append(items, h.resolve(ctx, f, mb, dom))
	}
	c.JSON(http.StatusOK, gin.H{"data": items, "total": int64(len(items)), "page": 1, "page_size": len(items)})
}

func (h *forwarderHandler) create(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	mb, dom, err := h.loadMailbox(ctx, c.Param("mbid"), claims)
	if err != nil {
		h.writeErr(c, err)
		return
	}
	var req forwarderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	if req.Type != "alias" && req.Type != "external" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_type"})
		return
	}
	if req.Type == "alias" && req.LocalPart == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alias_requires_local_part"})
		return
	}
	if req.Target == "" {
		req.Target = mb.LocalPart + "@" + dom.Name
	}
	f := &models.EmailForwarder{
		ID:        ids.NewULID(),
		MailboxID: &mb.ID,
		DomainID:  dom.ID,
		Type:      req.Type,
		Target:    req.Target,
		Enabled:   true,
		ManagedBy: "m6.5",
	}
	if req.Type == "alias" {
		lp := req.LocalPart
		f.LocalPart = &lp
	}
	if err := h.cfg.Forwarders.Create(ctx, f); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusCreated, h.resolve(ctx, *f, mb, dom))
}

func (h *forwarderHandler) del(c *gin.Context) {
	ctx := c.Request.Context()
	claims := ginctx.Claims(c)
	f, err := h.cfg.Forwarders.FindByID(ctx, c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if f.MailboxID == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "domain_scoped_forwarder"})
		return
	}
	_, _, err = h.loadMailbox(ctx, *f.MailboxID, claims)
	if err != nil {
		h.writeErr(c, err)
		return
	}
	if err := h.cfg.Forwarders.Delete(ctx, f.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *forwarderHandler) resolve(_ context.Context, f models.EmailForwarder, mb *models.Mailbox, dom *models.Domain) forwarderResponse {
	lp := ""
	if f.LocalPart != nil {
		lp = *f.LocalPart
	}
	return forwarderResponse{
		ID:           f.ID,
		MailboxID:    func() string { if f.MailboxID != nil { return *f.MailboxID }; return "" }(),
		MailboxEmail: mb.LocalPart + "@" + dom.Name,
		DomainID:     f.DomainID,
		DomainName:   dom.Name,
		Type:         f.Type,
		LocalPart:    lp,
		Target:       f.Target,
		Enabled:      f.Enabled,
		CreatedAt:    f.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (h *forwarderHandler) writeErr(c *gin.Context, err error) {
	if isNotFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if errors.Is(err, errMailboxForbidden) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
}
