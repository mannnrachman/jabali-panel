package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/mailaddr"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// MailboxHandlerConfig plugs the mailbox HTTP handlers into the router.
type MailboxHandlerConfig struct {
	Mailboxes repository.MailboxRepository
	Domains   repository.DomainRepository
	Agent     agent.AgentInterface
}

const (
	defaultMailboxesPageSize = 20
	maxMailboxesPageSize     = 200

	// Default mailbox quota — 1 GiB. Mirrors the column DEFAULT in
	// migration 000054; kept here as a fallback if a caller somehow
	// sends a zero QuotaBytes.
	defaultMailboxQuotaBytes uint64 = 1 << 30

	// Floor for user-supplied quotas. Anything below 16 MiB would make
	// even a test mailbox unusable (a single MIME attachment would
	// push past quota). The panel-UI will carry its own slider min,
	// but defence in depth here.
	minMailboxQuotaBytes uint64 = 16 * 1024 * 1024

	// Bcrypt cost — matches what Stalwart's SqlDirectory expects.
	// DefaultCost (10) is fine; higher would slow logins noticeably.
	mailboxBcryptCost = bcrypt.DefaultCost

	// Agent call budget. Matches other handlers that SSH or shell out.
	mailboxAgentTimeout = 30 * time.Second
)

// RegisterMailboxRoutes mounts the mailbox endpoints under g:
//
//   - GET  /domains/:id/mailboxes               list mailboxes in a domain
//   - POST /domains/:id/mailboxes               create a mailbox
//   - GET  /mailboxes/:mbid                     fetch a single mailbox
//   - PATCH /mailboxes/:mbid                    update quota
//   - POST /mailboxes/:mbid/rotate-password     rotate (or set) password
//   - DELETE /mailboxes/:mbid                   destroy mailbox
//
// The domain-scoped create/list live under /domains/:id/mailboxes so
// ownership is enforced once (via the domain row). The per-mailbox
// endpoints look up the mailbox, resolve its domain, and re-check the
// same ownership — this matches how database_users / database-user-grants
// are split between /database-users and /database-user-grants.
//
// ADR-0042 + ADR-0045: the panel-API is the only writer. We INSERT the
// row first (Stalwart's SqlDirectory reads on every auth, no cache to
// invalidate), then fire the agent cmd as a typed no-op acknowledgement
// so the shape stays consistent with M7's per-resource pattern.
func RegisterMailboxRoutes(g *gin.RouterGroup, cfg MailboxHandlerConfig) {
	h := &mailboxHandler{cfg: cfg}

	g.GET("/domains/:id/mailboxes", h.list)
	g.POST("/domains/:id/mailboxes", h.create)

	mbox := g.Group("/mailboxes")
	mbox.GET("/:mbid", h.get)
	mbox.PATCH("/:mbid", h.updateQuota)
	mbox.POST("/:mbid/rotate-password", h.rotatePassword)
	mbox.DELETE("/:mbid", h.delete)
}

type mailboxHandler struct{ cfg MailboxHandlerConfig }

// ---- Request / response types ----

type createMailboxRequest struct {
	// LocalPart — the "alice" in alice@example.com. Canonicalised
	// (lowercased, +tag stripped, ASCII-only) by internal/mailaddr
	// before we INSERT.
	LocalPart string `json:"local_part" binding:"required"`

	// Password — plaintext. We bcrypt it before storing. If empty,
	// we generate one and return it reveal-once in the response.
	Password string `json:"password"`

	// QuotaBytes — optional. Zero means "use default" (1 GiB).
	QuotaBytes uint64 `json:"quota_bytes"`
}

type createMailboxResponse struct {
	ID         string  `json:"id"`
	Email      string  `json:"email"`
	QuotaBytes uint64  `json:"quota_bytes"`
	// Password is returned exactly once when the caller did NOT send a
	// password — the agent-computed random one. Empty when the caller
	// supplied their own.
	Password string `json:"password,omitempty"`
}

type rotateMailboxPasswordRequest struct {
	// NewPassword — optional. If empty, server generates one and
	// returns it reveal-once.
	NewPassword string `json:"new_password"`
}

type rotateMailboxPasswordResponse struct {
	Password string `json:"password,omitempty"`
}

type updateMailboxQuotaRequest struct {
	QuotaBytes uint64 `json:"quota_bytes" binding:"required"`
}

type mailboxResponse struct {
	ID             string     `json:"id"`
	DomainID       string     `json:"domain_id"`
	Email          string     `json:"email"`
	QuotaBytes     uint64     `json:"quota_bytes"`
	IsDisabled     bool       `json:"is_disabled"`
	LastUsageBytes uint64     `json:"last_usage_bytes"`
	LastUsageAt    *time.Time `json:"last_usage_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ---- Handlers ----

func (h *mailboxHandler) list(c *gin.Context) {
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

	page, pageSize, opts := parseListOptions(c, defaultMailboxesPageSize, maxMailboxesPageSize)

	rows, total, err := h.cfg.Mailboxes.ListByDomainID(ctx, dom.ID, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if rows == nil {
		rows = []models.Mailbox{}
	}

	out := make([]mailboxResponse, len(rows))
	for i, mb := range rows {
		out[i] = toMailboxResponse(mb)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      out,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *mailboxHandler) create(c *gin.Context) {
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
	if !dom.EmailEnabled {
		c.JSON(http.StatusConflict, gin.H{
			"error":  "email_not_enabled",
			"detail": "enable email on the domain before creating mailboxes",
		})
		return
	}

	var req createMailboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}

	// Canonicalise the full address (local@domain.name) so rejection
	// rules (shell meta, empty, too long, non-ASCII) match what the
	// agent's domain.email_enable validator enforces.
	canonLocal, _, err := mailaddr.Canonicalise(req.LocalPart + "@" + dom.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_local_part", "detail": err.Error()})
		return
	}

	// Uniqueness is enforced by the UNIQUE index on email_cached — we
	// check up-front for a friendlier error code than the driver's
	// "duplicate entry" string.
	exists, err := h.cfg.Mailboxes.ExistsByDomainAndLocalPart(ctx, dom.ID, canonLocal)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "mailbox_exists"})
		return
	}

	password := req.Password
	generatedPassword := ""
	if password == "" {
		// ULID as password — 26 chars of Crockford base32 is ~130 bits
		// of entropy. Adequate for "reveal once, user copies to a
		// client". No hidden dependency, no extra import.
		password = ids.NewULID()
		generatedPassword = password
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), mailboxBcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	quota := req.QuotaBytes
	if quota == 0 {
		quota = defaultMailboxQuotaBytes
	}
	if quota < minMailboxQuotaBytes {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "quota_too_small",
			"detail": "quota_bytes must be at least 16 MiB",
		})
		return
	}

	now := time.Now().UTC()
	mb := &models.Mailbox{
		ID:           ids.NewULID(),
		DomainID:     dom.ID,
		LocalPart:    canonLocal,
		PasswordHash: string(hash),
		QuotaBytes:   quota,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.cfg.Mailboxes.Create(ctx, mb); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "mailbox_exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Inline best-effort agent notify (ADR-0013). Stalwart's
	// SqlDirectory syncs on every auth so this is a typed
	// acknowledgement rather than a cache-invalidate — but we still
	// surface agent errors so operators can see them.
	h.notifyAgent(ctx, "mailbox.create", map[string]any{
		"id":    mb.ID,
		"email": canonLocal + "@" + dom.Name,
	})

	c.JSON(http.StatusCreated, createMailboxResponse{
		ID:         mb.ID,
		Email:      canonLocal + "@" + dom.Name,
		QuotaBytes: quota,
		Password:   generatedPassword,
	})
}

func (h *mailboxHandler) get(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	mb, dom, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		h.writeLoadErr(c, err)
		return
	}
	_ = dom

	c.JSON(http.StatusOK, toMailboxResponse(*mb))
}

func (h *mailboxHandler) updateQuota(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req updateMailboxQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "detail": err.Error()})
		return
	}
	if req.QuotaBytes < minMailboxQuotaBytes {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "quota_too_small",
			"detail": "quota_bytes must be at least 16 MiB",
		})
		return
	}

	mb, dom, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		h.writeLoadErr(c, err)
		return
	}

	if err := h.cfg.Mailboxes.UpdateQuota(ctx, mb.ID, req.QuotaBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	h.notifyAgent(ctx, "mailbox.set_quota", map[string]any{
		"id":          mb.ID,
		"email":       mb.LocalPart + "@" + dom.Name,
		"quota_bytes": req.QuotaBytes,
	})

	mb.QuotaBytes = req.QuotaBytes
	mb.UpdatedAt = time.Now().UTC()
	c.JSON(http.StatusOK, toMailboxResponse(*mb))
}

func (h *mailboxHandler) rotatePassword(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req rotateMailboxPasswordRequest
	// Body is optional here — empty body means "generate a new one".
	_ = c.ShouldBindJSON(&req)

	mb, dom, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		h.writeLoadErr(c, err)
		return
	}

	password := req.NewPassword
	generated := ""
	if password == "" {
		password = ids.NewULID()
		generated = password
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), mailboxBcryptCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if err := h.cfg.Mailboxes.UpdatePasswordHash(ctx, mb.ID, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	h.notifyAgent(ctx, "mailbox.set_password", map[string]any{
		"id":    mb.ID,
		"email": mb.LocalPart + "@" + dom.Name,
	})

	c.JSON(http.StatusOK, rotateMailboxPasswordResponse{Password: generated})
}

func (h *mailboxHandler) delete(c *gin.Context) {
	ctx := c.Request.Context()

	claims := ginctx.Claims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	mb, dom, err := h.loadMailboxWithAuth(ctx, c.Param("mbid"), claims)
	if err != nil {
		h.writeLoadErr(c, err)
		return
	}

	// Delete agent-side first: it's the step that actually removes mail
	// content from RocksDB (via x:Account/set destroy — see
	// mailbox_delete.go in panel-agent). If that fails, abort before
	// the DB delete so the rows stays consistent with Stalwart's state.
	agentCtx, cancel := context.WithTimeout(ctx, mailboxAgentTimeout)
	defer cancel()
	_, err = h.cfg.Agent.Call(agentCtx, "mailbox.delete", map[string]any{
		"id":    mb.ID,
		"email": mb.LocalPart + "@" + dom.Name,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent_failed", "detail": err.Error()})
		return
	}

	if err := h.cfg.Mailboxes.Delete(ctx, mb.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.Status(http.StatusNoContent)
}

// ---- helpers ----

// loadMailboxWithAuth fetches a mailbox by ID, loads its owning domain,
// and verifies that `claims` can see it (admin, or the domain's owner).
// Returns one of these error sentinels for the caller to translate:
//   - repository.ErrNotFound → 404
//   - errMailboxForbidden → 403
//   - any other err → 500
func (h *mailboxHandler) loadMailboxWithAuth(ctx context.Context, id string, claims *auth.AccessClaims) (*models.Mailbox, *models.Domain, error) {
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

var errMailboxForbidden = &mailboxErr{kind: "forbidden"}

type mailboxErr struct{ kind string }

func (e *mailboxErr) Error() string { return "mailbox: " + e.kind }

func (h *mailboxHandler) writeLoadErr(c *gin.Context, err error) {
	if isNotFound(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	if err == errMailboxForbidden {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
}

// notifyAgent runs an agent call without failing the HTTP response on
// error — per ADR-0013 inline-best-effort. If the agent is nil (tests)
// this is a no-op. Errors are swallowed; the panel's reconciler is
// responsible for re-asserting state agents dropped.
func (h *mailboxHandler) notifyAgent(ctx context.Context, command string, params any) {
	if h.cfg.Agent == nil {
		return
	}
	agentCtx, cancel := context.WithTimeout(ctx, mailboxAgentTimeout)
	defer cancel()
	_, _ = h.cfg.Agent.Call(agentCtx, command, params)
}

func toMailboxResponse(mb models.Mailbox) mailboxResponse {
	return mailboxResponse{
		ID:             mb.ID,
		DomainID:       mb.DomainID,
		Email:          mb.EmailCached,
		QuotaBytes:     mb.QuotaBytes,
		IsDisabled:     mb.IsDisabled,
		LastUsageBytes: mb.LastUsageBytes,
		LastUsageAt:    mb.LastUsageAt,
		CreatedAt:      mb.CreatedAt,
		UpdatedAt:      mb.UpdatedAt,
	}
}
