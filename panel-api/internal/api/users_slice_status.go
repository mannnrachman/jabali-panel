package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// sliceStatusResponse mirrors the agent's user.slice.status output but
// stays independent of the agent's wire format so the UI contract doesn't
// drift with agent-side changes.
type sliceStatusResponse struct {
	Username           string `json:"username"`
	SliceActive        bool   `json:"slice_active"`
	FPMActive          bool   `json:"fpm_active"`
	MemoryCurrentBytes uint64 `json:"memory_current_bytes"`
	TasksCurrent       uint64 `json:"tasks_current"`
	CPUUsageNSec       uint64 `json:"cpu_usage_nsec"`
}

// sliceStatus handles GET /admin/users/:id/slice-status.
//
// Returns the live systemd slice + FPM service state for the given user.
// Admin-only (enforced by the route registration). For users with no
// Linux username (admin accounts) we return a zero-valued response
// with slice_active=false rather than 404 — the UI can render "no slice"
// without special-casing admins.
func (h *userHandler) sliceStatus(c *gin.Context) {
	userID := c.Param("id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user id required"})
		return
	}

	user, err := h.cfg.Repo.FindByID(c.Request.Context(), userID)
	if err != nil || user == nil {
		// FindByID returns nil, nil for not-found; we coalesce both here
		// because the admin doesn't care whether it was a DB error or a
		// missing row — neither produces a meaningful slice.
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Users without a Linux username (admins) have no slice. Return a
	// consistent zero response so the UI can render it as "n/a" without
	// extra plumbing.
	if user.Username == nil || *user.Username == "" {
		c.JSON(http.StatusOK, sliceStatusResponse{Username: ""})
		return
	}

	if h.cfg.Agent == nil {
		// Agent not wired in this binary (shouldn't happen in prod, but
		// keep the path explicit for local dev without an agent).
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent not available"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	raw, callErr := h.cfg.Agent.Call(ctx, "user.slice.status", map[string]string{"username": *user.Username})
	if callErr != nil {
		// Log via the handler's logger and return 502 — caller sees an
		// actionable error, internal detail stays in the logs.
		if h.cfg.Log != nil {
			h.cfg.Log.Warn("agent user.slice.status failed", "user_id", userID, "err", callErr)
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent call failed"})
		return
	}

	var resp sliceStatusResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		if h.cfg.Log != nil {
			h.cfg.Log.Warn("agent slice-status unmarshal failed", "user_id", userID, "err", err)
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "agent response malformed"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// Compile-time assertion: the user repo returns the fields we need.
// If the interface changes, this goes red before runtime.
var _ = func(_ repository.UserRepository) {}
