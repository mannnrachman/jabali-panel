package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/reconciler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

type DomainHandlerConfig struct {
	Domains    repository.DomainRepository
	Users      repository.UserRepository
	SSLCerts   repository.SSLCertificateRepository
	Packages   repository.PackageRepository
	Agent      agent.AgentInterface
	Reconciler *reconciler.Reconciler
	// DNSZones + DNSRecords feed the auto-enable-email path on create.
	// Both optional — when unset, create proceeds without flipping email
	// on (matches the pre-auto-enable behaviour). The explicit
	// /domains/:id/email endpoint is wired via DomainEmailHandlerConfig
	// separately and is the retry path for domains that skip auto-enable
	// or hit an error during create.
	DNSZones   repository.DNSZoneRepository
	DNSRecords repository.DNSRecordRepository
	// ManagedIPs is the M24 IP-pool repo. Optional: when nil, the
	// listen_ipv*_id PATCH path is rejected with 503 (the pool is the
	// source of truth) and GET responses skip the denormalized
	// listen_ipv4 / listen_ipv6 nested objects.
	ManagedIPs repository.ManagedIPRepository
}

const (
	defaultDomainsPageSize = 20
	maxDomainsPageSize     = 200
)

// Security validation patterns
var (
	// Domain name validation regex - RFC 1035 compliant
	domainNameRe = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)
	// HTML tag detection for XSS prevention
	htmlTagRe = regexp.MustCompile(`<[^>]*>`)
)

func RegisterDomainRoutes(g *gin.RouterGroup, cfg DomainHandlerConfig) {
	h := &domainHandler{cfg: cfg}
	domains := g.Group("/domains")
	domains.GET("", h.list)
	domains.POST("", h.create)
	domains.GET("/:id", h.get)
	domains.PATCH("/:id", h.update)
	domains.DELETE("/:id", h.delete)
}

type domainHandler struct{ cfg DomainHandlerConfig }

type createDomainRequest struct {
	Name    string `json:"name" binding:"required"`
	UserID  string `json:"user_id"`
	DocRoot string `json:"doc_root"`
}

// validateDomainName validates domain name for security and RFC compliance
func validateDomainName(s string) error {
	// Check for empty or whitespace
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("domain name cannot be empty")
	}

	// Check for whitespace (potential injection)
	if strings.ContainsAny(s, " \t\n\r") {
		return fmt.Errorf("domain name contains invalid whitespace characters")
	}

	// Check length limits per RFC 1035
	if len(s) > 253 {
		return fmt.Errorf("domain name exceeds 253 character limit")
	}

	// Check for HTML tags (XSS prevention)
	if htmlTagRe.MatchString(s) {
		return fmt.Errorf("domain name contains invalid HTML characters")
	}

	// Check for path traversal attempts
	if strings.Contains(s, "..") || strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return fmt.Errorf("domain name contains invalid path characters")
	}

	// RFC 1035 compliance check
	if !domainNameRe.MatchString(s) {
		return fmt.Errorf("domain name is not a valid FQDN (requires at least two labels and 2+ letter TLD)")
	}

	return nil
}

// validateDocumentRoot validates document root path to prevent path traversal
func validateDocumentRoot(docRoot, username, domainName string) error {
	if docRoot == "" {
		return nil // Will use default
	}

	// Must be under user's home directory
	expectedPrefix := "/home/" + username + "/"
	if !strings.HasPrefix(docRoot, expectedPrefix) {
		return fmt.Errorf("document root must be under user's home directory")
	}

	// Check for path traversal attempts
	if strings.Contains(docRoot, "..") {
		return fmt.Errorf("document root contains invalid path traversal sequences")
	}

	return nil
}

type updateDomainRequest struct {
	IsEnabled             *bool                 `json:"is_enabled,omitempty"`
	NginxCustomDirectives *string               `json:"nginx_custom_directives,omitempty"`
	RedirectAllTo         *string               `json:"redirect_all_to,omitempty"`
	RedirectAllType       *string               `json:"redirect_all_type,omitempty"`
	PageRedirects         *models.PageRedirects `json:"page_redirects,omitempty"`
	NginxRules            *models.NginxRules    `json:"nginx_rules,omitempty"`
	IndexPriority         *string               `json:"index_priority,omitempty"`
	// M24: per-domain IP binding. nullableUint64 distinguishes
	// "absent in PATCH" (don't touch the column) from "explicitly null"
	// (clear binding → fall back to server default for the family) from
	// "set to ID" (rebind). PATCH `{}`-only callers retain prior
	// behaviour exactly.
	ListenIPv4ID nullableUint64 `json:"listen_ipv4_id,omitempty"`
	ListenIPv6ID nullableUint64 `json:"listen_ipv6_id,omitempty"`
}

// nullableUint64 is the M24 wrapper that lets a PATCH body distinguish
// "field absent" from "field explicitly null". Gin's binding only invokes
// UnmarshalJSON when the key is present in the body, so Set stays false
// for absent fields. JSON null unmarshals to Set=true, Value=nil. JSON
// number unmarshals to Set=true, Value=&n. Any other JSON shape returns
// an error so the handler can 422.
type nullableUint64 struct {
	Set   bool
	Value *uint64
}

func (n *nullableUint64) UnmarshalJSON(data []byte) error {
	n.Set = true
	if string(data) == "null" {
		n.Value = nil
		return nil
	}
	var v uint64
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("listen_ipv*_id must be a positive integer or null: %w", err)
	}
	n.Value = &v
	return nil
}

// sslBadge is the nested SSL-cert summary embedded in domain list rows so
// the admin UI can differentiate self-signed from Let's Encrypt at a glance.
type sslBadge struct {
	Status    string     `json:"status"`
	Issuer    *string    `json:"issuer,omitempty"`
	IssuedAt  *time.Time `json:"issued_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// domainListRow wraps a Domain with the optional SSL badge. Embedded so
// existing consumers of the flat shape keep working.
type domainListRow struct {
	models.Domain
	SSL *sslBadge `json:"ssl,omitempty"`
	// Username is the owning hosting user's Linux account name, batched
	// onto each row so the admin Domains table can show a meaningful
	// owner column without a per-row lookup. nil when the owner can't be
	// resolved (deleted user, admin-only row).
	Username *string `json:"username,omitempty"`
	// Denormalized listen-IP summaries — UI shows the address string
	// without a second roundtrip per row. Always populated when
	// ManagedIPs is wired: explicit binding ⇒ that row's address; null
	// binding ⇒ family default address. nil only when the family default
	// itself is missing (fresh install before a v6 was added etc.).
	ListenIPv4 *ipSummary `json:"listen_ipv4,omitempty"`
	ListenIPv6 *ipSummary `json:"listen_ipv6,omitempty"`
}

// ipSummary is the denormalized {id, address} blob the UI consumes for
// the per-domain listen IP. id may be 0 when this is a fall-through
// "use server default" case where no managed_ip row exists for the family.
type ipSummary struct {
	ID      uint64 `json:"id"`
	Address string `json:"address"`
}

func (h *domainHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	page, pageSize, opts := parseListOptions(c, defaultDomainsPageSize, maxDomainsPageSize)

	var domains []models.Domain
	var total int64
	var err error

	if claims.IsAdmin {
		domains, total, err = h.cfg.Domains.List(c.Request.Context(), opts)
	} else {
		domains, total, err = h.cfg.Domains.ListByUserID(c.Request.Context(), claims.UserID, opts)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if domains == nil {
		domains = []models.Domain{}
	}

	// Enrich with SSL badge via a single batch lookup. Skipped cleanly if
	// SSLCerts isn't wired (e.g. early-boot tests) so the list still works.
	rows := make([]domainListRow, len(domains))
	for i := range domains {
		rows[i] = domainListRow{Domain: domains[i]}
	}
	if h.cfg.SSLCerts != nil && len(domains) > 0 {
		domainIDs := make([]string, len(domains))
		for i := range domains {
			domainIDs[i] = domains[i].ID
		}
		certs, certErr := h.cfg.SSLCerts.FindByDomainIDs(c.Request.Context(), domainIDs)
		if certErr == nil {
			certMap := make(map[string]*models.SSLCertificate, len(certs))
			for i := range certs {
				certMap[certs[i].DomainID] = &certs[i]
			}
			for i := range rows {
				if cert := certMap[rows[i].ID]; cert != nil {
					rows[i].SSL = sslBadgeFromCert(cert)
				}
			}
		}
		// On SSL lookup error we drop the badge silently — list response
		// still ships flat domain data rather than 500ing.
	}

	// Denormalize the owning user's Linux username onto each row so the
	// admin table can show a meaningful owner. Single batch lookup; on
	// error we drop the field rather than 500ing — the row's user_id is
	// still on the wire as a fallback.
	if h.cfg.Users != nil && len(domains) > 0 {
		userIDs := make([]string, 0, len(domains))
		seen := make(map[string]struct{}, len(domains))
		for i := range domains {
			if _, ok := seen[domains[i].UserID]; ok {
				continue
			}
			seen[domains[i].UserID] = struct{}{}
			userIDs = append(userIDs, domains[i].UserID)
		}
		users, userErr := h.cfg.Users.FindByIDs(c.Request.Context(), userIDs)
		if userErr == nil {
			usernameByID := make(map[string]*string, len(users))
			for i := range users {
				usernameByID[users[i].ID] = users[i].Username
			}
			for i := range rows {
				rows[i].Username = usernameByID[rows[i].UserID]
			}
		}
	}

	// M24: denormalize listen_ipv4 / listen_ipv6 onto each row. Pool is
	// capped at 100 (Step 2), so a single ListAll is cheaper than N
	// FindByID calls. Errors silently drop the field rather than 500;
	// the UI's per-IP page is the recovery path.
	if h.cfg.ManagedIPs != nil && len(domains) > 0 {
		ips, ipErr := h.cfg.ManagedIPs.ListAll(c.Request.Context())
		if ipErr == nil {
			ipByID := make(map[uint64]*models.ManagedIP, len(ips))
			var defaultV4, defaultV6 *models.ManagedIP
			for i := range ips {
				ipByID[ips[i].ID] = &ips[i]
				if ips[i].IsDefault {
					switch ips[i].Family {
					case "ipv4":
						defaultV4 = &ips[i]
					case "ipv6":
						defaultV6 = &ips[i]
					}
				}
			}
			for i := range rows {
				rows[i].ListenIPv4 = pickListenSummary(rows[i].ListenIPv4ID, ipByID, defaultV4)
				rows[i].ListenIPv6 = pickListenSummary(rows[i].ListenIPv6ID, ipByID, defaultV6)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// pickListenSummary resolves a domain row's listen_ipv*_id to the
// denormalized {id,address} blob using the prefetched batch maps.
// Falls back to the family default when the binding is null; returns
// nil only when no default is seeded for that family.
func pickListenSummary(id *uint64, byID map[uint64]*models.ManagedIP, def *models.ManagedIP) *ipSummary {
	if id != nil {
		if row, ok := byID[*id]; ok {
			return &ipSummary{ID: row.ID, Address: row.Address}
		}
	}
	if def != nil {
		return &ipSummary{ID: def.ID, Address: def.Address}
	}
	return nil
}

// enrichDomainResponse returns a domainListRow with the SSL badge and
// listen-IP denormalization filled in for a single Domain — used by GET
// /domains/:id and the PATCH response. The list path inlines its own
// loop that batches IP lookups.
func (h *domainHandler) enrichDomainResponse(ctx context.Context, d models.Domain) domainListRow {
	row := domainListRow{Domain: d}
	if h.cfg.SSLCerts != nil {
		certs, err := h.cfg.SSLCerts.FindByDomainIDs(ctx, []string{d.ID})
		if err == nil {
			for i := range certs {
				if certs[i].DomainID == d.ID {
					row.SSL = sslBadgeFromCert(&certs[i])
					break
				}
			}
		}
	}
	if h.cfg.ManagedIPs != nil {
		row.ListenIPv4 = h.resolveListenSummary(ctx, d.ListenIPv4ID, "ipv4")
		row.ListenIPv6 = h.resolveListenSummary(ctx, d.ListenIPv6ID, "ipv6")
	}
	return row
}

// resolveListenSummary fetches the {id,address} blob for a domain's
// listen_ipv*_id binding. Explicit binding ⇒ the bound row; nil binding
// ⇒ the family default. Returns nil only when neither resolves (e.g.
// the operator never seeded a v6 default).
func (h *domainHandler) resolveListenSummary(ctx context.Context, id *uint64, family string) *ipSummary {
	if id != nil {
		row, err := h.cfg.ManagedIPs.FindByID(ctx, *id)
		if err == nil {
			return &ipSummary{ID: row.ID, Address: row.Address}
		}
		// Per F-H-2: FK RESTRICT means this should be unreachable, but
		// if it does happen we fall through to default rather than emit
		// a null. The operator UI surfaces a separate "missing IP"
		// warning via the IP manager pages, not the per-domain GET.
	}
	row, err := h.cfg.ManagedIPs.FindDefaultByFamily(ctx, family)
	if err != nil {
		return nil
	}
	return &ipSummary{ID: row.ID, Address: row.Address}
}

// sslBadgeFromCert maps a cert row to the nested badge, filling Issuer
// based on status so the UI doesn't have to encode the label logic.
func sslBadgeFromCert(cert *models.SSLCertificate) *sslBadge {
	b := &sslBadge{
		Status:    cert.Status,
		IssuedAt:  cert.IssuedAt,
		ExpiresAt: cert.ExpiresAt,
	}
	switch cert.Status {
	case models.SSLStatusSelfSigned:
		s := "Self-signed"
		b.Issuer = &s
	case models.SSLStatusIssued, models.SSLStatusRenewing:
		s := "Let's Encrypt"
		b.Issuer = &s
	}
	return b
}

func (h *domainHandler) get(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, h.enrichDomainResponse(c.Request.Context(), *domain))
}

func (h *domainHandler) create(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req createDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	// SECURITY: Validate domain name to prevent XSS and path traversal
	if err := validateDomainName(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_domain_name",
			"detail": err.Error(),
		})
		return
	}

	// Sanitize domain name by removing any potential HTML tags
	req.Name = htmlTagRe.ReplaceAllString(req.Name, "")

	targetUserID := req.UserID
	if !claims.IsAdmin {
		targetUserID = claims.UserID
	}
	if targetUserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	ctx := c.Request.Context()

	user, err := h.cfg.Users.FindByID(ctx, targetUserID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Admins are panel-only — they have no /home/<name>, so domains
	// can't be hosted under them. Bad request, not authz failure.
	if user.IsAdmin {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "admin_cannot_host",
			"detail": "admin users are panel-only — create a regular user to host domains",
		})
		return
	}

	// Username should always be set for non-admin users.
	if user.Username == nil || *user.Username == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Quota check.
	if user.PackageID != nil && *user.PackageID != "" {
		count, err := h.cfg.Domains.CountByUserID(ctx, targetUserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		pkg, err := h.cfg.Packages.FindByID(ctx, *user.PackageID)
		if err == nil && pkg.MaxDomains > 0 && count >= int64(pkg.MaxDomains) {
			c.JSON(http.StatusConflict, gin.H{"error": "domain_quota_exceeded"})
			return
		}
	}

	// SECURITY: Validate custom document root path
	if err := validateDocumentRoot(req.DocRoot, *user.Username, req.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_document_root",
			"detail": err.Error(),
		})
		return
	}

	docRoot := req.DocRoot
	if docRoot == "" {
		// Per-domain subtree under /home/<user>/domains/<name>/ so sibling
		// paths like logs/, ssl/, backups/ can live alongside public_html
		// without polluting the user's home.
		// SECURITY: Domain name is now validated, safe to use in path construction
		docRoot = "/home/" + *user.Username + "/domains/" + req.Name + "/public_html"
	}

	now := time.Now().UTC()
	domain := &models.Domain{
		ID:        ids.NewULID(),
		UserID:    targetUserID,
		Name:      req.Name,
		DocRoot:   docRoot,
		IsEnabled: true,
		SSLEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := h.cfg.Domains.Create(ctx, domain); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "domain_already_exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Attempt SSL inline (30s timeout): try ACME first with fallback to self-signed.
	// Never errors out — just logs; cert state is already in DB.
	if h.cfg.Reconciler != nil {
		inlineCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		h.cfg.Reconciler.ReconcileSSLInline(inlineCtx, domain)
		cancel()
	}

	// Auto-enable email. Best-effort per ADR-0013: a failure here (agent
	// down, Stalwart refuses the name, DNS sync hiccup) degrades back to
	// the pre-auto-enable model — email_enabled stays 0 and the operator
	// sees the UI's "Enable email" retry switch on the domain's Email
	// tab. DNS-autoconfig warnings aren't returned in the create
	// response (that would change the wire shape); they're surfaced to
	// the operator on the next GET /domains/:id/email poll, which
	// computes live DNS status anyway. Hard errors go to slog.
	if h.cfg.Agent != nil && h.cfg.DNSZones != nil && h.cfg.DNSRecords != nil {
		if _, _, warnings, err := EnableDomainEmailInline(ctx, enableDomainEmailDeps{
			Agent:         h.cfg.Agent,
			Domains:       h.cfg.Domains,
			DNSZones:      h.cfg.DNSZones,
			DNSRecords:    h.cfg.DNSRecords,
			SSLCerts:      h.cfg.SSLCerts,
			SSLReconciler: h.cfg.Reconciler,
		}, domain); err != nil {
			slog.Warn("auto-enable email failed during domain.create (operator can retry from UI)",
				"domain_id", domain.ID, "domain", domain.Name, "err", err)
		} else if len(warnings) > 0 {
			slog.Info("auto-enable email DNS autoconfig warnings",
				"domain_id", domain.ID, "domain", domain.Name, "warnings", warnings)
		}
	}

	// Schedule reconciliation. The reconciler will converge the domain's
	// OS-level state (nginx vhost, PHP pool, etc.) with the DB state.
	// This is non-blocking and out-of-band.
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	// EnableDomainEmailInline mutated domain in place on success so the
	// response already carries email_enabled=true, dkim_selector, etc.
	c.JSON(http.StatusCreated, domain)
}

func (h *domainHandler) update(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req updateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	ctx := c.Request.Context()

	if req.IsEnabled != nil && *req.IsEnabled != domain.IsEnabled {
		domain.IsEnabled = *req.IsEnabled
	}

	if req.NginxCustomDirectives != nil {
		if msg := validateNginxDirectives(*req.NginxCustomDirectives); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		domain.NginxCustomDirectives = req.NginxCustomDirectives
	}

	if req.RedirectAllTo != nil {
		trimmed := strings.TrimSpace(*req.RedirectAllTo)
		if trimmed == "" {
			domain.RedirectAllTo = nil
		} else {
			if err := validateRedirectURL(trimmed); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			domain.RedirectAllTo = &trimmed
		}
	}

	if req.RedirectAllType != nil {
		trimmed := strings.TrimSpace(*req.RedirectAllType)
		if trimmed == "" {
			domain.RedirectAllType = nil
		} else if !isValidRedirectType(trimmed) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid redirect type"})
			return
		} else {
			domain.RedirectAllType = &trimmed
		}
	}

	if req.PageRedirects != nil {
		if err := validatePageRedirects(*req.PageRedirects); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		domain.PageRedirects = *req.PageRedirects
	}

	if req.IndexPriority != nil {
		p := strings.TrimSpace(*req.IndexPriority)
		if !isValidIndexPriority(p) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_index_priority"})
			return
		}
		domain.IndexPriority = p
	}

	// M24: per-domain IP binding — validate FK + family + (for non-admin)
	// is_user_selectable. We resolve and validate before issuing any DB
	// write so a bad ipv4 doesn't half-succeed against ipv6.
	listenUpd, ipErr := h.resolveListenIPUpdate(ctx, claims.IsAdmin, req.ListenIPv4ID, req.ListenIPv6ID)
	if ipErr != nil {
		c.JSON(ipErr.Status, gin.H{"error": ipErr.Code, "detail": ipErr.Detail})
		return
	}

	domain.UpdatedAt = time.Now().UTC()
	if err := h.cfg.Domains.Update(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Listen IPs are written via dedicated repo method — Domain.Update's
	// allowlist intentionally excludes listen_ipv*_id. Mirror the in-memory
	// struct so the response reflects the new binding.
	if listenUpd.ChangeIPv4 || listenUpd.ChangeIPv6 {
		if err := h.cfg.Domains.SetListenIPs(ctx, domain.ID, listenUpd); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
		if listenUpd.ChangeIPv4 {
			domain.ListenIPv4ID = listenUpd.IPv4ID
		}
		if listenUpd.ChangeIPv6 {
			domain.ListenIPv6ID = listenUpd.IPv6ID
		}
	}

	// Schedule reconciliation to sync the domain state with the agent.
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	c.JSON(http.StatusOK, h.enrichDomainResponse(ctx, *domain))
}

// listenIPError carries the (status, code, detail) tuple from
// resolveListenIPUpdate so the caller can emit the right JSON shape
// without resorting to multi-error sentinel checks.
type listenIPError struct {
	Status int
	Code   string
	Detail string
}

// resolveListenIPUpdate validates the listen_ipv*_id PATCH fields and
// builds the repository.DomainListenIPs payload. Returns ChangeIPv4 /
// ChangeIPv6 only for fields that were actually present in the request.
//
// Rules:
//   - ManagedIPs repo not wired → 503 (the FK target table is the
//     authoritative source; we won't accept blind writes).
//   - IPv4 field carries an IPv6 address (or vice-versa) → 400.
//   - Referenced row missing → 404.
//   - Non-admin caller picking a row with is_user_selectable=false → 403.
//   - Explicit null → unbind (fall back to server default at render time).
func (h *domainHandler) resolveListenIPUpdate(ctx context.Context, isAdmin bool, v4, v6 nullableUint64) (repository.DomainListenIPs, *listenIPError) {
	upd := repository.DomainListenIPs{}
	if !v4.Set && !v6.Set {
		return upd, nil
	}
	if h.cfg.ManagedIPs == nil {
		return upd, &listenIPError{
			Status: http.StatusServiceUnavailable,
			Code:   "ip_pool_unavailable",
			Detail: "managed IP pool is not configured on this server",
		}
	}
	if v4.Set {
		if v4.Value == nil {
			upd.ChangeIPv4 = true
			upd.IPv4ID = nil
		} else {
			row, err := h.cfg.ManagedIPs.FindByID(ctx, *v4.Value)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return upd, &listenIPError{Status: http.StatusNotFound, Code: "listen_ipv4_not_found"}
				}
				return upd, &listenIPError{Status: http.StatusInternalServerError, Code: "internal"}
			}
			if row.Family != "ipv4" {
				return upd, &listenIPError{Status: http.StatusBadRequest, Code: "listen_ipv4_family_mismatch", Detail: "managed_ip " + strconv.FormatUint(row.ID, 10) + " is not an IPv4 address"}
			}
			if !isAdmin && !row.IsUserSelectable {
				return upd, &listenIPError{Status: http.StatusForbidden, Code: "listen_ipv4_not_user_selectable", Detail: "this IPv4 is not enabled for user selection — ask an administrator"}
			}
			upd.ChangeIPv4 = true
			upd.IPv4ID = &row.ID
		}
	}
	if v6.Set {
		if v6.Value == nil {
			upd.ChangeIPv6 = true
			upd.IPv6ID = nil
		} else {
			row, err := h.cfg.ManagedIPs.FindByID(ctx, *v6.Value)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return upd, &listenIPError{Status: http.StatusNotFound, Code: "listen_ipv6_not_found"}
				}
				return upd, &listenIPError{Status: http.StatusInternalServerError, Code: "internal"}
			}
			if row.Family != "ipv6" {
				return upd, &listenIPError{Status: http.StatusBadRequest, Code: "listen_ipv6_family_mismatch", Detail: "managed_ip " + strconv.FormatUint(row.ID, 10) + " is not an IPv6 address"}
			}
			if !isAdmin && !row.IsUserSelectable {
				return upd, &listenIPError{Status: http.StatusForbidden, Code: "listen_ipv6_not_user_selectable", Detail: "this IPv6 is not enabled for user selection — ask an administrator"}
			}
			upd.ChangeIPv6 = true
			upd.IPv6ID = &row.ID
		}
	}
	return upd, nil
}

func (h *domainHandler) delete(c *gin.Context) {
	ctx := c.Request.Context()
	domain, err := h.cfg.Domains.FindByID(ctx, c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Capture name BEFORE deleting — once the DB row is gone, the
	// reconciler can't look it up by ID. We pass the name to
	// ReconcileDeleted which targets the agent-side teardown directly.
	name := domain.Name
	if err := h.cfg.Domains.Delete(ctx, domain.ID); err != nil {
		// M6.4 (ADR-0048): the panel-primary row is delete-protected at
		// the repo layer. Translate to 403 with a specific error code
		// so the panel UI can render a tooltip instead of a generic 500.
		if errors.Is(err, repository.ErrCannotDeletePanelPrimary) {
			c.JSON(http.StatusForbidden, gin.H{"error": "panel_primary_protected"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Tear down OS-level resources out-of-band. Best-effort: the user
	// sees the row gone immediately; if agent teardown fails, the next
	// ReconcileAll tick logs the orphan for ops to investigate.
	if h.cfg.Reconciler != nil {
		go h.cfg.Reconciler.ReconcileDeleted(context.Background(), name)
	}

	c.Status(http.StatusNoContent)
}

// allowedNginxDirectives is a per-line allowlist of nginx directives that users
// can safely include in the server {} block. This is a FIRST line of defense;
// the agent still runs nginx -t before applying, so malformed input is caught there.
var allowedNginxDirectives = map[string]struct{}{
	// Headers/response
	"add_header":        {},
	"add_trailer":       {},
	"expires":           {},
	"etag":              {},
	"if_modified_since": {},
	"return":            {},
	// Rewrites
	"rewrite":    {},
	"set":        {},
	"if":         {},
	"break":      {},
	"error_page": {},
	// Proxy
	"proxy_pass":              {},
	"proxy_set_header":        {},
	"proxy_hide_header":       {},
	"proxy_pass_header":       {},
	"proxy_buffering":         {},
	"proxy_buffer_size":       {},
	"proxy_buffers":           {},
	"proxy_http_version":      {},
	"proxy_read_timeout":      {},
	"proxy_connect_timeout":   {},
	"proxy_send_timeout":      {},
	"proxy_redirect":          {},
	"proxy_ssl_verify":        {},
	"proxy_ssl_server_name":   {},
	"proxy_request_buffering": {},
	"proxy_cache_bypass":      {},
	"proxy_no_cache":          {},
	// Body/upload
	"client_max_body_size":    {},
	"client_body_buffer_size": {},
	"client_body_timeout":     {},
	"client_header_timeout":   {},
	// FastCGI
	"fastcgi_read_timeout": {},
	"fastcgi_send_timeout": {},
	"fastcgi_buffer_size":  {},
	"fastcgi_buffers":      {},
	"fastcgi_param":        {},
	// Static/locations
	"location":             {},
	"try_files":            {},
	"index":                {},
	"autoindex":            {},
	"autoindex_exact_size": {},
	"autoindex_localtime":  {},
	"sub_filter":           {},
	"sub_filter_once":      {},
	"sub_filter_types":     {},
	"charset":              {},
	"default_type":         {},
	"types":                {},
	"log_not_found":        {},
	// Access
	"allow":                {},
	"deny":                 {},
	"satisfy":              {},
	"auth_basic":           {},
	"auth_basic_user_file": {},
	"limit_except":         {},
	"limit_req":            {},
	"limit_req_zone":       {},
	"limit_conn":           {},
	// Gzip
	"gzip":            {},
	"gzip_types":      {},
	"gzip_min_length": {},
	"gzip_comp_level": {},
	"gzip_vary":       {},
	"gzip_disable":    {},
	"gzip_proxied":    {},
	// Caching
	"open_file_cache":          {},
	"open_file_cache_valid":    {},
	"open_file_cache_min_uses": {},
	"open_file_cache_errors":   {},
}

func validateNginxDirectives(directives string) string {
	// Reject if input contains null bytes (binary/injection attempt).
	if strings.ContainsRune(directives, '\x00') {
		return "forbidden directive: null byte detected"
	}

	lines := strings.Split(directives, "\n")
	blockDepth := 0
	maxNestingDepth := 3

	for _, line := range lines {
		// Strip comments while respecting strings.
		cleaned := stripComments(line)
		cleaned = strings.TrimSpace(cleaned)

		// Empty lines are allowed.
		if cleaned == "" {
			continue
		}

		// Count opening and closing braces in this line (respecting strings).
		opens, closes := countBraces(cleaned)

		for i := 0; i < opens; i++ {
			blockDepth++
			if blockDepth > maxNestingDepth {
				return "forbidden directive: nesting depth exceeded (max " + strconv.Itoa(maxNestingDepth) + ")"
			}
		}
		for i := 0; i < closes; i++ {
			blockDepth--
			if blockDepth < 0 {
				return "forbidden directive: unbalanced braces (extra closing })"
			}
		}

		// Handle lines that are purely {, }, or {}.
		if cleaned == "{" || cleaned == "}" || cleaned == "{}" {
			continue
		}

		// Extract the directive name (first token).
		directive := extractDirective(cleaned)
		if directive == "" {
			continue
		}

		// Skip if the first token is a brace.
		if directive == "{" || directive == "}" {
			continue
		}

		// Normalize to lowercase and check against allowlist.
		directive = strings.ToLower(directive)
		if _, allowed := allowedNginxDirectives[directive]; !allowed {
			return "forbidden directive: " + directive
		}
	}

	// Ensure braces are balanced.
	if blockDepth != 0 {
		return "forbidden directive: unbalanced braces (unclosed {)"
	}

	return ""
}

// countBraces counts opening and closing braces in a line, respecting quoted strings.
func countBraces(line string) (opens, closes int) {
	inSingleQuote := false
	inDoubleQuote := false

	for _, ch := range line {
		switch ch {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '{':
			if !inSingleQuote && !inDoubleQuote {
				opens++
			}
		case '}':
			if !inSingleQuote && !inDoubleQuote {
				closes++
			}
		}
	}
	return
}

// stripComments removes everything from # onwards, but respects # inside
// single or double-quoted strings.
func stripComments(line string) string {
	inSingleQuote := false
	inDoubleQuote := false

	for i, ch := range line {
		switch ch {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '#':
			if !inSingleQuote && !inDoubleQuote {
				return line[:i]
			}
		}
	}
	return line
}

// extractDirective returns the first whitespace-delimited token from a line.
func extractDirective(line string) string {
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func validateRedirectURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid destination URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("destination URL must use http or https scheme")
	}
	if u.Host == "" {
		return fmt.Errorf("destination URL must have a host")
	}
	return nil
}

func isValidRedirectType(s string) bool {
	switch s {
	case "301", "302", "307", "308":
		return true
	default:
		return false
	}
}

func validatePageRedirects(prs models.PageRedirects) error {
	if len(prs) > 100 {
		return fmt.Errorf("too many redirects (max 100)")
	}
	for i, pr := range prs {
		if !strings.HasPrefix(pr.Source, "/") {
			return fmt.Errorf("entry %d: source must start with /", i)
		}
		if strings.ContainsAny(pr.Source, "\n\x00") {
			return fmt.Errorf("entry %d: source contains invalid chars", i)
		}
		if err := validateRedirectURL(pr.Destination); err != nil {
			return fmt.Errorf("entry %d: invalid page redirect destination: %w", i, err)
		}
		if !isValidRedirectType(pr.Type) {
			return fmt.Errorf("entry %d: invalid type for page redirect: %s", i, pr.Type)
		}
		// Wildcard only supports 301 and 302
		if pr.Wildcard && pr.Type != "301" && pr.Type != "302" {
			return fmt.Errorf("entry %d: wildcard redirects only support 301 or 302", i)
		}
	}
	return nil
}


func isValidNginxRuleType(s string) bool {
	switch s {
	case "custom_header", "rewrite", "proxy_pass", "ip_access", "php_setting", "max_upload_size":
		return true
	}
	return false
}

// validateNginxRules checks each rule has the fields required by its
// Type. Field-level constraints (e.g. header name format, valid CIDR)
// are intentionally lenient — nginx -t on the agent is the final check.

// validateNginxRules checks each rule has the fields required by its
// Type. Field-level constraints (e.g. header name format, valid CIDR)
// are intentionally lenient — nginx -t on the agent is the final check.
func validateNginxRules(rules models.NginxRules) error {
	if len(rules) > 50 {
		return fmt.Errorf("too many rules (max 50)")
	}
	for i, r := range rules {
		if !isValidNginxRuleType(r.Type) {
			return fmt.Errorf("rule %d: unknown type %q", i, r.Type)
		}
		switch r.Type {
		case "custom_header":
			if r.Name == "" {
				return fmt.Errorf("rule %d: header name required", i)
			}
			if r.Value == "" {
			return fmt.Errorf("rule %d: header value required", i)
		}
		if strings.ContainsAny(r.Name, " \t\n\r:;") {
				return fmt.Errorf("rule %d: invalid chars in header name", i)
			}
		case "rewrite":
			if r.Pattern == "" || r.Replacement == "" {
				return fmt.Errorf("rule %d: pattern and replacement required", i)
			}
			switch r.Flag {
			case "", "last", "break", "redirect", "permanent":
			default:
				return fmt.Errorf("rule %d: invalid flag %q", i, r.Flag)
			}
		case "proxy_pass":
			if r.Path == "" || r.Target == "" {
				return fmt.Errorf("rule %d: path and target required", i)
			}
			if !strings.HasPrefix(r.Target, "http://") && !strings.HasPrefix(r.Target, "https://") {
				return fmt.Errorf("rule %d: target must be an http(s) URL", i)
			}
		case "ip_access":
			if r.Path == "" {
				return fmt.Errorf("rule %d: path required", i)
			}
			if r.Mode != "allow_list" && r.Mode != "deny_list" {
				return fmt.Errorf("rule %d: mode must be allow_list or deny_list", i)
			}
			if len(r.IPs) == 0 {
				return fmt.Errorf("rule %d: at least one IP required", i)
			}
		case "php_setting":
			if r.Name == "" || r.Value == "" {
				return fmt.Errorf("rule %d: name and value required", i)
			}
		case "max_upload_size":
			if r.Size == "" {
				return fmt.Errorf("rule %d: size required", i)
			}
		}
		// Forbid control characters everywhere to prevent newline injection into vhost
		allText := r.Name + r.Value + r.Pattern + r.Replacement + r.Target + r.Path + r.Size
		for _, c := range allText {
			if c < 32 && c != '\t' {
				return fmt.Errorf("rule %d: contains invalid control chars", i)
			}
		}
		for _, ip := range r.IPs {
			if strings.ContainsAny(ip, " \t\n\r") {
				return fmt.Errorf("rule %d: invalid chars in IP %q", i, ip)
			}
		}
	}
	return nil
}

func isValidIndexPriority(s string) bool {
	switch s {
	case "html_first", "php_first", "html_only", "php_only", "full":
		return true
	}
	return false
}

func domainLinuxUser(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return "user"
}
