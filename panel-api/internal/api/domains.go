package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
}

const (
	defaultDomainsPageSize = 20
	maxDomainsPageSize     = 200
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

type updateDomainRequest struct {
	IsEnabled             *bool                 `json:"is_enabled,omitempty"`
	NginxCustomDirectives *string               `json:"nginx_custom_directives,omitempty"`
	RedirectAllTo         *string               `json:"redirect_all_to,omitempty"`
	RedirectAllType       *string               `json:"redirect_all_type,omitempty"`
	PageRedirects         *models.PageRedirects `json:"page_redirects,omitempty"`
	NginxRules            *models.NginxRules    `json:"nginx_rules,omitempty"`
	IndexPriority         *string               `json:"index_priority,omitempty"`
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

	c.JSON(http.StatusOK, gin.H{
		"data":      rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
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
	c.JSON(http.StatusOK, domain)
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

	docRoot := req.DocRoot
	if docRoot == "" {
		// Per-domain subtree under /home/<user>/domains/<name>/ so sibling
		// paths like logs/, ssl/, backups/ can live alongside public_html
		// without polluting the user's home.
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

	domain.UpdatedAt = time.Now().UTC()
	if err := h.cfg.Domains.Update(ctx, domain); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation to sync the domain state with the agent.
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	c.JSON(http.StatusOK, domain)
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
