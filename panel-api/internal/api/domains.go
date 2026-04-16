package api

import (
	"context"
	"fmt"
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
	Packages   repository.PackageRepository
	Agent      agent.AgentInterface
	Reconciler *reconciler.Reconciler
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
	IndexPriority         *string               `json:"index_priority,omitempty"`
}

func (h *domainHandler) list(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultDomainsPageSize)))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxDomainsPageSize {
		pageSize = defaultDomainsPageSize
	}
	offset := (page - 1) * pageSize

	var domains []models.Domain
	var total int64
	var err error

	if claims.IsAdmin {
		domains, total, err = h.cfg.Domains.List(c.Request.Context(), offset, pageSize)
	} else {
		domains, total, err = h.cfg.Domains.ListByUserID(c.Request.Context(), claims.UserID, offset, pageSize)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	if domains == nil {
		domains = []models.Domain{}
	}
	c.JSON(http.StatusOK, gin.H{
		"data":      domains,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
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

	// Schedule reconciliation. The reconciler will converge the domain's
	// OS-level state (nginx vhost, PHP pool, etc.) with the DB state.
	// This is non-blocking and out-of-band.
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

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

// isValidIndexPriority returns true for the enum values the agent knows
// how to map to nginx `index` directives.
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
