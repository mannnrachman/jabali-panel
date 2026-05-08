package reconciler

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/limits"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// M18 — per-user resource limits + per-domain nginx rate limits.
//
// Two convergence passes:
//
//   ReconcileUserLimits  — for every user with a package or override,
//     compute the effective limits and call user.limits.apply. The
//     agent is idempotent: a no-change reapply is cheap (compares
//     drop-in content on disk, skips the write) but we still make the
//     RPC every pass for drift detection.
//
//   ReconcileNginxRateLimits — collect every domain with a non-zero
//     rate_limit_rps or connection_limit, send the whole set to
//     nginx.ratelimits.apply which renders the single fragment at
//     /etc/nginx/conf.d/00-jabali-ratelimits.conf.

// WithPackages injects the package repository — required for M18
// ReconcileUserLimits to hydrate the per-user effective limits.
func (r *Reconciler) WithPackages(packages repository.PackageRepository) *Reconciler {
	r.packages = packages
	return r
}

// WithLimitOverrides injects the user-limit-override repository.
func (r *Reconciler) WithLimitOverrides(overrides repository.UserLimitOverrideRepository) *Reconciler {
	r.limitOverrides = overrides
	return r
}

// WithQuotaMount records the filesystem mount path containing /home,
// passed on every user.limits.* agent call so the agent uses an
// explicit `setquota` mount path (never -a).
func (r *Reconciler) WithQuotaMount(mount string) *Reconciler {
	r.quotaMount = mount
	return r
}

// ReconcileUserLimits walks every user in the DB, resolves their
// effective limits, and calls user.limits.apply on the agent. Any
// single-user failure is logged and skipped — this pass must NOT
// short-circuit a whole reconcile cycle for one bad user.
//
// Runs after ReconcileUsers (not explicitly — our existing pipeline
// provisions users on demand during domain reconcile). The net effect
// is the same: every user that has a working Linux account on this
// host gets its limits converged every tick.
//
// Silently no-ops if dependencies aren't wired yet (pre-M18 deployments
// or tests that don't care about this codepath).
func (r *Reconciler) ReconcileUserLimits(ctx context.Context) {
	if r.packages == nil || r.limitOverrides == nil || r.users == nil {
		return
	}

	// Fetch all users — at reasonable host sizes (<10k) this is a single
	// round-trip. For larger deployments we'd paginate.
	users, _, err := r.users.List(ctx, repository.ListOptions{Limit: 10000})
	if err != nil {
		r.log.Warn("reconcile user-limits: list users failed", "err", err)
		return
	}

	// Read the global disk-quota toggle once per pass. When false we
	// still apply cgroup limits (cpu/mem/io/tasks) but pass quota_mount=""
	// to the agent so the setquota step short-circuits — matches the
	// `QuotaMount empty skips setquota` convention used by user.limits.*.
	// See server_settings.disk_quota_enabled (migration 000071).
	quotaMount := r.quotaMount
	if r.serverSettings != nil {
		if s, sErr := r.serverSettings.Get(ctx); sErr == nil && s != nil && !s.DiskQuotaEnabled {
			quotaMount = ""
		}
	}

	// Batch-load overrides in one query to avoid N+1 lookups.
	ovAll, err := r.limitOverrides.ListAll(ctx)
	if err != nil {
		r.log.Warn("reconcile user-limits: list overrides failed", "err", err)
		ovAll = nil
	}
	overridesByUser := make(map[string]*limits.OverrideLimits, len(ovAll))
	for i := range ovAll {
		o := ovAll[i]
		overridesByUser[o.UserID] = &limits.OverrideLimits{
			DiskQuotaMB:     o.DiskQuotaMB,
			CPUQuotaPercent: o.CPUQuotaPercent,
			MemoryLimitMB:   o.MemoryLimitMB,
			IOReadMbps:      o.IOReadMbps,
			IOWriteMbps:     o.IOWriteMbps,
			MaxTasks:        o.MaxTasks,
		}
	}

	for i := range users {
		u := &users[i]
		if u.Username == nil || *u.Username == "" {
			continue // no Linux account yet — skip, will pick up next pass
		}

		var pkgL *limits.PackageLimits
		if u.PackageID != nil && *u.PackageID != "" {
			pkg, err := r.packages.FindByID(ctx, *u.PackageID)
			if err == nil {
				pkgL = &limits.PackageLimits{
					DiskQuotaMB:     pkg.DiskQuotaMB,
					CPUQuotaPercent: pkg.CPUQuotaPercent,
					MemoryLimitMB:   pkg.MemoryLimitMB,
					IOReadMbps:      pkg.IOReadMbps,
					IOWriteMbps:     pkg.IOWriteMbps,
					MaxTasks:        pkg.MaxTasks,
				}
			}
		}
		effective := limits.Resolve(pkgL, overridesByUser[u.ID])

		// No package + no override → user must be unlimited.
		// Dispatch user.limits.clear instead of skipping: skipping
		// leaves stale POSIX quotas + cgroup drop-ins from a
		// previously-assigned package on the host, so the user
		// keeps hitting the old quota wall. The clear handler is
		// idempotent (setquota -d is a no-op when no quota set;
		// the cgroup drop-in delete is conditional on existence).
		if pkgL == nil && overridesByUser[u.ID] == nil {
			ctxCall, cancel := context.WithTimeout(ctx, 10*time.Second)
			if _, err := r.agent.Call(ctxCall, "user.limits.clear", map[string]any{
				"username":    *u.Username,
				"quota_mount": r.quotaMount,
			}); err != nil {
				r.log.WarnContext(ctx, "user.limits.clear failed",
					"username", *u.Username, "err", err)
			}
			cancel()
			continue
		}

		ctxCall, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := r.agent.Call(ctxCall, "user.limits.apply", map[string]any{
			"username":          *u.Username,
			"disk_quota_mb":     effective.DiskQuotaMB,
			"cpu_quota_percent": effective.CPUQuotaPercent,
			"memory_limit_mb":   effective.MemoryLimitMB,
			"io_read_mbps":      effective.IOReadMbps,
			"io_write_mbps":     effective.IOWriteMbps,
			"max_tasks":         effective.MaxTasks,
			"quota_mount":       quotaMount,
		})
		cancel()
		if err != nil {
			r.log.Warn("reconcile user-limits: apply failed",
				"username", *u.Username, "quota_mount", quotaMount, "err", err)
		}
	}
}

// ReconcileNginxRateLimits renders the shared zone-declaration fragment
// at /etc/nginx/conf.d/00-jabali-ratelimits.conf by walking every
// domain with a non-zero rate_limit_rps or connection_limit and sending
// the bundle to nginx.ratelimits.apply on the agent.
//
// Per-vhost `limit_req` / `limit_conn` directives are emitted when
// domain.create renders the vhost (using BuildRateLimitDirectives from
// the agent package). That path is already on every domain convergence
// tick so no separate call is needed here — the fragment is the ONLY
// thing centralised at the reconciler level.
func (r *Reconciler) ReconcileNginxRateLimits(ctx context.Context) {
	if r.domains == nil {
		return
	}
	allDomains, _, err := r.domains.List(ctx, repository.ListOptions{Limit: 10000})
	if err != nil {
		r.log.Warn("reconcile nginx-ratelimits: list domains failed", "err", err)
		return
	}

	// Build the slice of domains with any non-zero limit — the agent
	// renders only what it's given (empty input = empty fragment, which
	// is a valid no-op file).
	type rateDomain struct {
		DomainID        string `json:"domain_id"`
		RateLimitRPS    uint32 `json:"rate_limit_rps"`
		ConnectionLimit uint32 `json:"connection_limit"`
	}
	bundle := make([]rateDomain, 0, len(allDomains))
	for i := range allDomains {
		d := &allDomains[i]
		if d.RateLimitRPS == 0 && d.ConnectionLimit == 0 {
			continue
		}
		bundle = append(bundle, rateDomain{
			DomainID:        d.ID,
			RateLimitRPS:    d.RateLimitRPS,
			ConnectionLimit: d.ConnectionLimit,
		})
	}

	ctxCall, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := r.agent.Call(ctxCall, "nginx.ratelimits.apply", map[string]any{
		"domains":      bundle,
		"zone_size_kb": 0, // 0 → agent default (10 MB)
	}); err != nil {
		r.log.Warn("reconcile nginx-ratelimits: apply failed", "err", err, "count", len(bundle))
		return
	}
	// Reload nginx only when at least one domain opted in — every
	// reload causes briefly-interrupted connections. No domains with
	// limits = no change to the fragment = no reload needed.
	if len(bundle) > 0 {
		reloadCtx, reloadCancel := context.WithTimeout(ctx, 10*time.Second)
		defer reloadCancel()
		if _, err := r.agent.Call(reloadCtx, "nginx.reload", nil); err != nil {
			r.log.Warn("reconcile nginx-ratelimits: reload failed", "err", err)
		}
	}
}

// Ensure any struct field additions here show up as compile errors
// in the main reconciler file — the Reconciler struct has the real
// type declarations and this file's helpers assume those fields exist.
var _ = fmt.Sprintf // keep fmt import stable even if future edits drop all its uses
