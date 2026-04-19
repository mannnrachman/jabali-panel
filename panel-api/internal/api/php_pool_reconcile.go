package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// reconcilePHPPoolViaAgent fires php.pool.apply against the agent for the
// given pool, then writes the resulting status back to the DB. Designed
// to be called inside `go ...` from request handlers so a user-driven
// version/config change converges immediately instead of waiting for the
// next reconciler tick.
//
// Used by both the admin /php-pools/:id PUT path and the user-driven
// POST /domains/:id/php-pool path so the two flows behave identically.
// Callers are expected to have already flipped pool.Status to "pending"
// (or its equivalent) before invoking — the helper itself overwrites
// status with "ready" on success or "error" on failure.
func reconcilePHPPoolViaAgent(
	ag agent.AgentInterface,
	users repository.UserRepository,
	overrides repository.PHPPoolIniOverrideRepository,
	pools repository.PHPPoolRepository,
	pool *models.PHPPool,
) {
	agentCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	user, err := users.FindByID(agentCtx, pool.UserID)
	if err != nil {
		slog.ErrorContext(agentCtx, "reconcilePHPPoolViaAgent: load user", "error", err, "pool_id", pool.ID)
		return
	}

	overridesList, err := overrides.ListByPool(agentCtx, pool.ID)
	if err != nil {
		slog.ErrorContext(agentCtx, "reconcilePHPPoolViaAgent: list overrides", "error", err, "pool_id", pool.ID)
		return
	}

	adminValues := []map[string]string{}
	adminFlags := []map[string]string{}
	for _, override := range overridesList {
		kv := map[string]string{"name": override.Directive, "value": override.Value}
		if override.Kind == "flag" {
			adminFlags = append(adminFlags, kv)
		} else {
			adminValues = append(adminValues, kv)
		}
	}

	_, err = ag.Call(agentCtx, "php.pool.apply", map[string]any{
		"username":                     user.Username,
		"php_version":                  pool.PHPVersion,
		"pm_mode":                      pool.PmMode,
		"pm_max_children":              pool.PmMaxChildren,
		"process_idle_timeout_seconds": pool.ProcessIdleTimeoutSeconds,
		"admin_values":                 adminValues,
		"admin_flags":                  adminFlags,
	})
	if err != nil {
		pool.Status = "error"
		errMsg := fmt.Sprintf("agent failed: %v", err)
		pool.LastError = &errMsg
		_ = pools.Update(agentCtx, pool)
		return
	}
	pool.Status = "ready"
	pool.LastError = nil
	_ = pools.Update(agentCtx, pool)
}
