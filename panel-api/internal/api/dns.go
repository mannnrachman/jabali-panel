package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DNSScheduler schedules domain reconciliation.
type DNSScheduler interface {
	Schedule(domainID string)
}

type DNSHandlerConfig struct {
	Domains    repository.DomainRepository
	Zones      repository.DNSZoneRepository
	Records    repository.DNSRecordRepository
	Reconciler DNSScheduler
}

func RegisterDNSRoutes(g *gin.RouterGroup, cfg DNSHandlerConfig) {
	h := &dnsHandler{cfg: cfg}

	// Zone + records scoped under a domain
	d := g.Group("/domains/:id/dns")
	d.GET("/zone", h.getZone)
	d.PATCH("/zone", h.updateZone)
	d.GET("/records", h.listRecords)
	d.POST("/records", h.createRecord)

	// Record-level operations don't need the domain id in the path
	rec := g.Group("/dns/records")
	rec.PATCH("/:recordId", h.updateRecord)
	rec.DELETE("/:recordId", h.deleteRecord)
}

type dnsHandler struct {
	cfg DNSHandlerConfig
}

type updateZoneRequest struct {
	RefreshSeconds *int `json:"refresh_seconds,omitempty"`
	RetrySeconds   *int `json:"retry_seconds,omitempty"`
	ExpireSeconds  *int `json:"expire_seconds,omitempty"`
	MinimumTTL     *int `json:"minimum_ttl,omitempty"`
	IsEnabled      *bool `json:"is_enabled,omitempty"`
}

type createRecordRequest struct {
	Name      string `json:"name" binding:"required"`
	Type      string `json:"type" binding:"required"`
	Content   string `json:"content" binding:"required"`
	TTL       *int   `json:"ttl,omitempty"`
	Priority  *int   `json:"priority,omitempty"`
	IsEnabled *bool  `json:"is_enabled,omitempty"`
}

type updateRecordRequest struct {
	Name      *string `json:"name,omitempty"`
	Type      *string `json:"type,omitempty"`
	Content   *string `json:"content,omitempty"`
	TTL       *int    `json:"ttl,omitempty"`
	Priority  *int    `json:"priority,omitempty"`
	IsEnabled *bool   `json:"is_enabled,omitempty"`
}

// loadDomainOwned fetches the domain by ID and enforces that the
// caller is either the owner or an admin. Returns nil if successful,
// otherwise responds to c and returns nil.
func (h *dnsHandler) loadDomainOwned(c *gin.Context, domainID string) *models.Domain {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return nil
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return nil
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return nil
	}

	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return nil
	}

	return domain
}

func (h *dnsHandler) getZone(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	if h.loadDomainOwned(c, domainID) == nil {
		return
	}

	zone, err := h.cfg.Zones.FindByDomainID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "zone_not_provisioned",
				"detail": "DNS zone not yet provisioned. The reconciler will create it on next sync.",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"zone": zone})
}

func (h *dnsHandler) updateZone(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	if h.loadDomainOwned(c, domainID) == nil {
		return
	}

	var req updateZoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "validation_failed",
			"detail": err.Error(),
		})
		return
	}

	zone, err := h.cfg.Zones.FindByDomainID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "zone_not_provisioned",
				"detail": "DNS zone not yet provisioned. The reconciler will create it on next sync.",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Apply updates with validation
	if req.RefreshSeconds != nil {
		if *req.RefreshSeconds < 60 || *req.RefreshSeconds > 86400 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "validation_failed",
				"detail": "refresh_seconds must be between 60 and 86400",
			})
			return
		}
		zone.RefreshSeconds = *req.RefreshSeconds
	}

	if req.RetrySeconds != nil {
		if *req.RetrySeconds < 60 || *req.RetrySeconds > 86400 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "validation_failed",
				"detail": "retry_seconds must be between 60 and 86400",
			})
			return
		}
		zone.RetrySeconds = *req.RetrySeconds
	}

	if req.ExpireSeconds != nil {
		if *req.ExpireSeconds < 3600 || *req.ExpireSeconds > 2419200 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "validation_failed",
				"detail": "expire_seconds must be between 3600 and 2419200",
			})
			return
		}
		zone.ExpireSeconds = *req.ExpireSeconds
	}

	if req.MinimumTTL != nil {
		if *req.MinimumTTL < 60 || *req.MinimumTTL > 86400 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "validation_failed",
				"detail": "minimum_ttl must be between 60 and 86400",
			})
			return
		}
		zone.MinimumTTL = *req.MinimumTTL
	}

	if req.IsEnabled != nil {
		zone.IsEnabled = *req.IsEnabled
	}

	// Persist update
	if err := h.cfg.Zones.Update(c.Request.Context(), zone); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation to push fresh SOA
	h.cfg.Reconciler.Schedule(domainID)

	c.JSON(http.StatusOK, gin.H{"zone": zone})
}

func (h *dnsHandler) listRecords(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	if h.loadDomainOwned(c, domainID) == nil {
		return
	}

	// Load zone to get zone ID
	zone, err := h.cfg.Zones.FindByDomainID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "zone_not_provisioned",
				"detail": "DNS zone not yet provisioned. The reconciler will create it on next sync.",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	records, err := h.cfg.Records.ListByZoneID(c.Request.Context(), zone.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if records == nil {
		records = []models.DNSRecord{}
	}

	c.JSON(http.StatusOK, gin.H{"records": records})
}

func (h *dnsHandler) createRecord(c *gin.Context) {
	domainID := c.Param("id")

	// Load and authorize domain
	if h.loadDomainOwned(c, domainID) == nil {
		return
	}

	var req createRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "validation_failed",
			"detail": err.Error(),
		})
		return
	}

	// Load zone to get zone ID
	zone, err := h.cfg.Zones.FindByDomainID(c.Request.Context(), domainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "zone_not_provisioned",
				"detail": "DNS zone not yet provisioned. The reconciler will create it on next sync.",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Build record from request
	record := &models.DNSRecord{
		ID:     ids.NewULID(),
		ZoneID: zone.ID,
		Name:   req.Name,
		Type:   req.Type,
		Content: req.Content,
		TTL:    3600, // Default TTL
		Priority: 0,
		Managed: false,
		IsEnabled: true,
	}

	// Override defaults if provided
	if req.TTL != nil {
		record.TTL = *req.TTL
	}
	if req.Priority != nil {
		record.Priority = *req.Priority
	}
	if req.IsEnabled != nil {
		record.IsEnabled = *req.IsEnabled
	}

	// Validate record
	if err := validateDNSRecord(record); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_record",
			"detail": err.Error(),
		})
		return
	}

	// Persist record
	if err := h.cfg.Records.Create(c.Request.Context(), record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation
	h.cfg.Reconciler.Schedule(domainID)

	c.JSON(http.StatusCreated, gin.H{"record": record})
}

func (h *dnsHandler) updateRecord(c *gin.Context) {
	recordID := c.Param("recordId")

	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req updateRecordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "validation_failed",
			"detail": err.Error(),
		})
		return
	}

	// Load record
	record, err := h.cfg.Records.FindByID(c.Request.Context(), recordID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Load zone to get domain ID
	zone, err := h.cfg.Zones.FindByID(c.Request.Context(), record.ZoneID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Load domain to authorize
	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), zone.DomainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Check if record is managed and read-only (SOA/NS)
	if record.Managed && (record.Type == "SOA" || record.Type == "NS") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "record_managed",
			"detail": "Cannot modify managed SOA or NS records",
		})
		return
	}

	// Apply updates
	if req.Name != nil {
		record.Name = *req.Name
	}
	if req.Type != nil {
		record.Type = *req.Type
	}
	if req.Content != nil {
		record.Content = *req.Content
	}
	if req.TTL != nil {
		record.TTL = *req.TTL
	}
	if req.Priority != nil {
		record.Priority = *req.Priority
	}
	if req.IsEnabled != nil {
		record.IsEnabled = *req.IsEnabled
	}

	// Validate updated record
	if err := validateDNSRecord(record); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_record",
			"detail": err.Error(),
		})
		return
	}

	// Persist update
	if err := h.cfg.Records.Update(c.Request.Context(), record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation
	h.cfg.Reconciler.Schedule(zone.DomainID)

	c.JSON(http.StatusOK, gin.H{"record": record})
}

func (h *dnsHandler) deleteRecord(c *gin.Context) {
	recordID := c.Param("recordId")

	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	// Load record
	record, err := h.cfg.Records.FindByID(c.Request.Context(), recordID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Load zone to get domain ID
	zone, err := h.cfg.Zones.FindByID(c.Request.Context(), record.ZoneID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Load domain to authorize
	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), zone.DomainID)
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Check authorization
	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Check if record is managed and read-only (SOA/NS)
	if record.Managed && (record.Type == "SOA" || record.Type == "NS") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "record_managed",
			"detail": "Cannot delete managed SOA or NS records",
		})
		return
	}

	// Delete record
	if err := h.cfg.Records.Delete(c.Request.Context(), recordID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Schedule reconciliation
	h.cfg.Reconciler.Schedule(zone.DomainID)

	c.Status(http.StatusNoContent)
}

// Validation helpers

func isValidDNSType(t string) bool {
	switch strings.ToUpper(t) {
	case "A", "AAAA", "CNAME", "MX", "TXT", "NS":
		return true
	}
	return false
}

func validateDNSRecord(r *models.DNSRecord) error {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	r.Name = strings.TrimSpace(r.Name)
	r.Content = strings.TrimSpace(r.Content)

	if !isValidDNSType(r.Type) {
		return fmt.Errorf("unsupported record type %q (allowed: A, AAAA, CNAME, MX, TXT, NS)", r.Type)
	}
	if r.Name == "" {
		return fmt.Errorf("name required (use '@' for apex)")
	}
	if strings.ContainsAny(r.Name, " \t\n\r\x00") {
		return fmt.Errorf("name has invalid whitespace / control chars")
	}
	if r.TTL == 0 {
		r.TTL = 3600
	}
	if r.TTL < 60 || r.TTL > 604800 {
		return fmt.Errorf("ttl must be between 60 and 604800 seconds")
	}

	switch r.Type {
	case "A":
		ip := net.ParseIP(r.Content)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("A content must be an IPv4 address")
		}
	case "AAAA":
		ip := net.ParseIP(r.Content)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("AAAA content must be an IPv6 address")
		}
	case "CNAME", "NS":
		if r.Content == "" || strings.ContainsAny(r.Content, " \t\n\r\x00") {
			return fmt.Errorf("%s content must be a hostname", r.Type)
		}
	case "MX":
		if r.Content == "" {
			return fmt.Errorf("MX content must be a hostname (target mailserver)")
		}
		if r.Priority < 0 || r.Priority > 65535 {
			return fmt.Errorf("MX priority must be 0-65535")
		}
	case "TXT":
		if len(r.Content) > 4000 {
			return fmt.Errorf("TXT content exceeds 4000 chars")
		}
	}
	return nil
}
