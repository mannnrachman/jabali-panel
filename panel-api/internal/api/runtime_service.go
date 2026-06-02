package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/services"
)

// envKeyRe matches a POSIX environment variable name. Validated
// server-side so a direct API call can't bypass the UI's check and
// smuggle a malformed key down to the agent's unit-file renderer.
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type updateRuntimeServiceRequest struct {
	EntryPoint string            `json:"entry_point"`
	EnvVars    map[string]string `json:"env_vars"`
}

func forbidNonAdminDockerRuntime(c *gin.Context, claims *auth.AccessClaims, domain *models.Domain) bool {
	if domain.RuntimeType == models.RuntimeDocker && !claims.IsAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "docker_runtime_admin_only"})
		return true
	}
	return false
}

func (h *domainHandler) getRuntimeService(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if forbidNonAdminDockerRuntime(c, claims, domain) {
		return
	}

	if !models.RuntimeNeedsProxy(domain.RuntimeType) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "runtime_not_applicable",
			"detail": "domain uses PHP or static runtime, which do not run custom runtime processes",
		})
		return
	}

	ctx := c.Request.Context()

	// Find or auto-provision the runtime service row
	svc, err := h.cfg.RuntimeServices.FindByDomainID(ctx, domain.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Allocate a free port via the shared allocator (random probe
			// + in-flight reservation) instead of a linear DB scan.
			allocator := services.NewPortAllocator(h.cfg.RuntimeServices, 10000, 60000)
			port, aErr := allocator.Allocate(ctx)
			if aErr != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no_free_port"})
				return
			}
			defer allocator.Release(port)

			username := ""
			u, uErr := h.cfg.Users.FindByID(ctx, domain.UserID)
			if uErr == nil && u.Username != nil {
				username = *u.Username
			}

			now := time.Now().UTC()
			svc = &models.RuntimeService{
				ID:          ids.NewULID(),
				DomainID:    domain.ID,
				UserID:      domain.UserID,
				Runtime:     domain.RuntimeType,
				ListenPort:  port,
				Status:      models.RuntimeStatusPending,
				SystemdUnit: "jabali-rt-" + domain.Name + ".service",
				PidFile:     "/home/" + username + "/.config/systemd/user/jabali-rt-" + domain.Name + ".pid",
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := h.cfg.RuntimeServices.Create(ctx, svc); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_provision_runtime_service"})
				return
			}
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
			return
		}
	}

	// Resolve the owner's username for agent status/log queries. The
	// agent's runtime.status / runtime.logs handlers key off
	// {username, domain}, NOT systemd_unit.
	ownerName := ""
	if u, uErr := h.cfg.Users.FindByID(ctx, domain.UserID); uErr == nil && u.Username != nil {
		ownerName = *u.Username
	}

	// Fetch real-time status and logs from agent
	var agentStatus any
	var logs string

	if h.cfg.Agent != nil && ownerName != "" {
		// Real-time status query
		statusRes, sErr := h.cfg.Agent.Call(ctx, "runtime.status", map[string]any{
			"username": ownerName,
			"domain":   domain.Name,
		})
		if sErr == nil {
			var parsed map[string]any
			if jsonErr := json.Unmarshal(statusRes, &parsed); jsonErr == nil {
				agentStatus = parsed
			} else {
				agentStatus = string(statusRes)
			}
		}

		// Real-time logs query
		logsRes, lErr := h.cfg.Agent.Call(ctx, "runtime.logs", map[string]any{
			"username": ownerName,
			"domain":   domain.Name,
			"lines":    100,
		})
		if lErr == nil {
			var temp struct {
				Logs string `json:"logs"`
			}
			if jsonErr := json.Unmarshal(logsRes, &temp); jsonErr == nil {
				logs = temp.Logs
			} else {
				logs = string(logsRes)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"runtime_service": svc,
		"agent_status":    agentStatus,
		"logs":            logs,
	})
}

func (h *domainHandler) updateRuntimeService(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if forbidNonAdminDockerRuntime(c, claims, domain) {
		return
	}

	ctx := c.Request.Context()
	svc, err := h.cfg.RuntimeServices.FindByDomainID(ctx, domain.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "runtime_service_not_found"})
		return
	}

	var req updateRuntimeServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "validation_failed", "detail": err.Error()})
		return
	}

	// Validate env vars server-side. The UI checks this too, but a direct
	// API call must not be able to push a malformed key or a value with
	// newlines/quotes down to the agent's systemd unit renderer.
	for k, v := range req.EnvVars {
		if !envKeyRe.MatchString(k) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_env_key", "detail": k})
			return
		}
		if strings.ContainsAny(v, "\x00\n\r\"\\") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_env_value", "detail": k})
			return
		}
	}

	// Update fields
	svc.EntryPoint = req.EntryPoint
	if req.EnvVars != nil {
		svc.EnvVars = req.EnvVars
	}
	svc.UpdatedAt = time.Now().UTC()

	// If the service was failed, let's clear the error and set to pending so it redeploys
	svc.Status = models.RuntimeStatusPending
	svc.LastError = nil

	if err := h.cfg.RuntimeServices.Update(ctx, svc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_runtime_service"})
		return
	}

	// Reconcile changes out of band
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	c.JSON(http.StatusOK, svc)
}

func (h *domainHandler) restartRuntimeService(c *gin.Context) {
	claims := ginctx.Claims(c)
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	domain, err := h.cfg.Domains.FindByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "domain_not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
		return
	}

	if !claims.IsAdmin && domain.UserID != claims.UserID {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if forbidNonAdminDockerRuntime(c, claims, domain) {
		return
	}

	ctx := c.Request.Context()
	svc, err := h.cfg.RuntimeServices.FindByDomainID(ctx, domain.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "runtime_service_not_found"})
		return
	}

	// Reset status to pending to force a fresh apply / restart sequence
	svc.Status = models.RuntimeStatusPending
	svc.LastError = nil
	_ = h.cfg.RuntimeServices.Update(ctx, svc)

	// Trigger reconciler sync immediately
	if h.cfg.Reconciler != nil {
		h.cfg.Reconciler.Schedule(domain.ID)
	}

	c.JSON(http.StatusOK, gin.H{"status": "restart_scheduled"})
}
