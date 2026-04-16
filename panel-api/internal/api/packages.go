package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// PackageHandlerConfig plugs the hosting-package CRUD handlers into the router.
type PackageHandlerConfig struct {
	Repo repository.PackageRepository
}

const (
	defaultPackagesPageSize = 20
	maxPackagesPageSize     = 200
)

// RegisterPackageRoutes mounts /packages* under g (admin-only).
func RegisterPackageRoutes(g *gin.RouterGroup, cfg PackageHandlerConfig) {
	h := &packageHandler{cfg: cfg}

	pkgs := g.Group("/packages", middleware.RequireAdmin())
	pkgs.GET("", h.list)
	pkgs.POST("", h.create)
	pkgs.GET("/:id", h.get)
	pkgs.PATCH("/:id", h.update)
	pkgs.DELETE("/:id", h.delete)
}

type packageHandler struct{ cfg PackageHandlerConfig }

// ---- request / response ----

type createPackageRequest struct {
	Name             string `json:"name"               binding:"required"`
	DiskQuotaMB      uint32 `json:"disk_quota_mb"`
	BandwidthQuotaMB uint32 `json:"bandwidth_quota_mb"`
	MaxDomains       uint32 `json:"max_domains"`
	MaxEmailAccounts uint32 `json:"max_email_accounts"`
	MaxDatabases     uint32 `json:"max_databases"`
	MaxFTPAccounts   uint32 `json:"max_ftp_accounts"`
	SSHEnabled       bool   `json:"ssh_enabled"`
	CGIEnabled       bool   `json:"cgi_enabled"`
}

type updatePackageRequest struct {
	Name             *string `json:"name"`
	DiskQuotaMB      *uint32 `json:"disk_quota_mb"`
	BandwidthQuotaMB *uint32 `json:"bandwidth_quota_mb"`
	MaxDomains       *uint32 `json:"max_domains"`
	MaxEmailAccounts *uint32 `json:"max_email_accounts"`
	MaxDatabases     *uint32 `json:"max_databases"`
	MaxFTPAccounts   *uint32 `json:"max_ftp_accounts"`
	SSHEnabled       *bool   `json:"ssh_enabled"`
	CGIEnabled       *bool   `json:"cgi_enabled"`
}

// ---- handlers ----

func (h *packageHandler) list(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", strconv.Itoa(defaultPackagesPageSize)))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > maxPackagesPageSize {
		pageSize = defaultPackagesPageSize
	}
	offset := (page - 1) * pageSize

	pkgs, total, err := h.cfg.Repo.List(c.Request.Context(), offset, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":      pkgs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *packageHandler) create(c *gin.Context) {
	var req createPackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	now := time.Now().UTC()
	pkg := &models.HostingPackage{
		ID:               ids.NewULID(),
		Name:             req.Name,
		DiskQuotaMB:      req.DiskQuotaMB,
		BandwidthQuotaMB: req.BandwidthQuotaMB,
		MaxDomains:       req.MaxDomains,
		MaxEmailAccounts: req.MaxEmailAccounts,
		MaxDatabases:     req.MaxDatabases,
		MaxFTPAccounts:   req.MaxFTPAccounts,
		SSHEnabled:       req.SSHEnabled,
		CGIEnabled:       req.CGIEnabled,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := h.cfg.Repo.Create(c.Request.Context(), pkg); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "already_exists", "detail": "package name taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusCreated, pkg)
}

func (h *packageHandler) get(c *gin.Context) {
	pkg, err := h.cfg.Repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, pkg)
}

func (h *packageHandler) update(c *gin.Context) {
	pkg, err := h.cfg.Repo.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	var req updatePackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	if req.Name != nil {
		pkg.Name = *req.Name
	}
	if req.DiskQuotaMB != nil {
		pkg.DiskQuotaMB = *req.DiskQuotaMB
	}
	if req.BandwidthQuotaMB != nil {
		pkg.BandwidthQuotaMB = *req.BandwidthQuotaMB
	}
	if req.MaxDomains != nil {
		pkg.MaxDomains = *req.MaxDomains
	}
	if req.MaxEmailAccounts != nil {
		pkg.MaxEmailAccounts = *req.MaxEmailAccounts
	}
	if req.MaxDatabases != nil {
		pkg.MaxDatabases = *req.MaxDatabases
	}
	if req.MaxFTPAccounts != nil {
		pkg.MaxFTPAccounts = *req.MaxFTPAccounts
	}
	if req.SSHEnabled != nil {
		pkg.SSHEnabled = *req.SSHEnabled
	}
	if req.CGIEnabled != nil {
		pkg.CGIEnabled = *req.CGIEnabled
	}
	pkg.UpdatedAt = time.Now().UTC()

	if err := h.cfg.Repo.Update(c.Request.Context(), pkg); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "already_exists", "detail": "package name taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.JSON(http.StatusOK, pkg)
}

func (h *packageHandler) delete(c *gin.Context) {
	if err := h.cfg.Repo.Delete(c.Request.Context(), c.Param("id")); err != nil {
		if isNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}
	c.Status(http.StatusNoContent)
}

func isConflict(err error) bool {
	return errors.Is(err, repository.ErrConflict)
}

func isNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound)
}
