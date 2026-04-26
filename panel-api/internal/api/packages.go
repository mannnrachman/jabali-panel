package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/limits"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// PackageReconciler is the narrow surface the update handler needs from
// the reconciler — just the per-user SSH-keys re-run that translates
// the package's ssh_enabled flag into jabali-sftp group membership.
// Defined here so tests can supply a fake without pulling in the full
// reconciler.
type PackageReconciler interface {
	ReconcileSSHKeysForUser(ctx context.Context, userID string) error
}

// PackageHandlerConfig plugs the hosting-package CRUD handlers into the router.
type PackageHandlerConfig struct {
	Repo repository.PackageRepository
	// Users + Reconciler enable the post-update fan-out: when an admin
	// flips a package field that affects per-user state (today only
	// ssh_enabled, but the same hook covers future per-user-effective
	// fields), the handler enumerates users on this package and triggers
	// their reconciler so changes apply without waiting up to a minute
	// for the periodic sweep — and without forcing the operator to
	// re-save every user. Both nil-safe.
	Users      repository.UserRepository
	Reconciler PackageReconciler
	Log        *slog.Logger
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
	// M18 resource limits. Zero = unlimited on every field.
	CPUQuotaPercent  uint32 `json:"cpu_quota_percent"`
	MemoryLimitMB    uint32 `json:"memory_limit_mb"`
	IOReadMbps       uint32 `json:"io_read_mbps"`
	IOWriteMbps      uint32 `json:"io_write_mbps"`
	MaxTasks         uint32 `json:"max_tasks"`
	BandwidthQuotaMB uint32 `json:"bandwidth_quota_mb"`
	MaxDomains       uint32 `json:"max_domains"`
	MaxEmailAccounts uint32 `json:"max_email_accounts"`
	MaxDatabases     uint32 `json:"max_databases"`
	MaxFTPAccounts   uint32 `json:"max_ftp_accounts"`
	SSHEnabled       bool   `json:"ssh_enabled"`
	CGIEnabled       bool   `json:"cgi_enabled"`
	// M13: nspawn image pin (empty = use server default).
	NspawnImageVersion string `json:"nspawn_image_version"`
}

type updatePackageRequest struct {
	Name               *string `json:"name"`
	DiskQuotaMB        *uint32 `json:"disk_quota_mb"`
	CPUQuotaPercent    *uint32 `json:"cpu_quota_percent"`
	MemoryLimitMB      *uint32 `json:"memory_limit_mb"`
	IOReadMbps         *uint32 `json:"io_read_mbps"`
	IOWriteMbps        *uint32 `json:"io_write_mbps"`
	MaxTasks           *uint32 `json:"max_tasks"`
	BandwidthQuotaMB   *uint32 `json:"bandwidth_quota_mb"`
	MaxDomains         *uint32 `json:"max_domains"`
	MaxEmailAccounts   *uint32 `json:"max_email_accounts"`
	MaxDatabases       *uint32 `json:"max_databases"`
	MaxFTPAccounts     *uint32 `json:"max_ftp_accounts"`
	SSHEnabled         *bool   `json:"ssh_enabled"`
	CGIEnabled         *bool   `json:"cgi_enabled"`
	NspawnImageVersion *string `json:"nspawn_image_version"`
}

// ---- handlers ----

func (h *packageHandler) list(c *gin.Context) {
	page, pageSize, opts := parseListOptions(c, defaultPackagesPageSize, maxPackagesPageSize)

	pkgs, total, err := h.cfg.Repo.List(c.Request.Context(), opts)
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
		CPUQuotaPercent:  req.CPUQuotaPercent,
		MemoryLimitMB:    req.MemoryLimitMB,
		IOReadMbps:       req.IOReadMbps,
		IOWriteMbps:      req.IOWriteMbps,
		MaxTasks:         req.MaxTasks,
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
	if v := strings.TrimSpace(req.NspawnImageVersion); v != "" {
		if !isImageNamePattern(v) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "invalid_nspawn_image_version",
				"detail": "must match [a-z0-9-]+",
			})
			return
		}
		pkg.NspawnImageVersion = &v
	}

	if err := validatePackageLimits(pkg); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
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

// validatePackageLimits enforces the bounds from internal/limits on the
// M18 resource-limit fields before write. Runs at both create and
// update — the agent validates again as defense-in-depth, but returning
// a clean 422 here is much better UX than a 502-agent-error later.
func validatePackageLimits(pkg *models.HostingPackage) error {
	e := limits.EffectiveLimits{
		DiskQuotaMB:     pkg.DiskQuotaMB,
		CPUQuotaPercent: pkg.CPUQuotaPercent,
		MemoryLimitMB:   pkg.MemoryLimitMB,
		IOReadMbps:      pkg.IOReadMbps,
		IOWriteMbps:     pkg.IOWriteMbps,
		MaxTasks:        pkg.MaxTasks,
	}
	return e.Validate()
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

	// Snapshot the fields whose change requires a per-user reconcile
	// fan-out, so we can compare to the new values once Update returns
	// successfully. Read BEFORE the field copies overwrite pkg.
	prevSSHEnabled := pkg.SSHEnabled

	if req.Name != nil {
		pkg.Name = *req.Name
	}
	if req.DiskQuotaMB != nil {
		pkg.DiskQuotaMB = *req.DiskQuotaMB
	}
	if req.CPUQuotaPercent != nil {
		pkg.CPUQuotaPercent = *req.CPUQuotaPercent
	}
	if req.MemoryLimitMB != nil {
		pkg.MemoryLimitMB = *req.MemoryLimitMB
	}
	if req.IOReadMbps != nil {
		pkg.IOReadMbps = *req.IOReadMbps
	}
	if req.IOWriteMbps != nil {
		pkg.IOWriteMbps = *req.IOWriteMbps
	}
	if req.MaxTasks != nil {
		pkg.MaxTasks = *req.MaxTasks
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
	if req.NspawnImageVersion != nil {
		v := strings.TrimSpace(*req.NspawnImageVersion)
		if v == "" {
			pkg.NspawnImageVersion = nil
		} else {
			if !isImageNamePattern(v) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":  "invalid_nspawn_image_version",
					"detail": "must match [a-z0-9-]+",
				})
				return
			}
			pkg.NspawnImageVersion = &v
		}
	}
	pkg.UpdatedAt = time.Now().UTC()

	if err := validatePackageLimits(pkg); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	if err := h.cfg.Repo.Update(c.Request.Context(), pkg); err != nil {
		if isConflict(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "already_exists", "detail": "package name taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	// Fan out to every user on this package whenever ssh_enabled flipped.
	// Done in a detached goroutine + fresh background context so the
	// admin response isn't blocked by per-user agent calls (each user
	// triggers an ssh.user.{join,leave}_sftp_group + authorized_keys
	// rewrite). The 60s reconciler sweep is the safety net; this just
	// makes the change feel immediate.
	if req.SSHEnabled != nil && *req.SSHEnabled != prevSSHEnabled {
		h.fanOutSSHReconcile(pkg.ID)
	}

	c.JSON(http.StatusOK, pkg)
}

// fanOutSSHReconcile reconciles every user on the given package in a
// detached goroutine. Bounded list size (10k) matches the periodic
// sweep — anyone with > 10k users on a single package has bigger
// problems than this fan-out missing a tail.
//
// Errors are logged, never returned: the admin already got their 200,
// and the periodic sweep will catch any user we couldn't reach.
func (h *packageHandler) fanOutSSHReconcile(packageID string) {
	if h.cfg.Users == nil || h.cfg.Reconciler == nil {
		return
	}
	users := h.cfg.Users
	rec := h.cfg.Reconciler
	log := h.cfg.Log
	if log == nil {
		log = slog.Default()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		all, _, err := users.List(ctx, repository.ListOptions{Limit: 10000})
		if err != nil {
			log.Warn("package update: list users for ssh reconcile", "package_id", packageID, "err", err)
			return
		}
		count := 0
		for i := range all {
			u := &all[i]
			if u.PackageID == nil || *u.PackageID != packageID {
				continue
			}
			perCtx, perCancel := context.WithTimeout(ctx, 30*time.Second)
			if err := rec.ReconcileSSHKeysForUser(perCtx, u.ID); err != nil {
				log.Warn("package update: ssh reconcile user", "package_id", packageID, "user_id", u.ID, "err", err)
			} else {
				count++
			}
			perCancel()
		}
		log.Info("package update: ssh reconcile fan-out complete", "package_id", packageID, "users", count)
	}()
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
