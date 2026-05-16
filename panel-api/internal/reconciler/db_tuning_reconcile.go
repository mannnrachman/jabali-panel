package reconciler

// M46 Step 3 (ADR-0098): converge curated DB tuning. DB
// (db_tuning_settings) is the source of truth. The PUT handler applies
// immediately; this loop is the safety net — it re-applies any engine
// that still has rows with applied_at IS NULL (panel restarted
// mid-apply, or a write that never reached the agent).
//
// Gating on pending rows (not "every tick") is deliberate: a blanket
// re-apply would restart PostgreSQL every interval for restart-required
// keys. MariaDB's agent handler is byte-idempotent so it would be
// cheap there, but the pending-gate keeps both engines consistent and
// the steady state truly free.

import (
	"context"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/dbtuning"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// WithDBAdmin wires the M46 tuning/audit/jobs repo into the reconciler.
func (r *Reconciler) WithDBAdmin(repo repository.DBAdminRepository) *Reconciler {
	r.dbAdmin = repo
	return r
}

func (r *Reconciler) reconcileDBTuning(ctx context.Context) {
	if r.dbAdmin == nil || r.agent == nil {
		return
	}
	rows, err := r.dbAdmin.ListAllTuning(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "reconcile db tuning: list failed", "err", err)
		return
	}
	type bucket struct {
		settings map[string]string
		pending  bool
	}
	byEngine := map[string]*bucket{}
	for _, row := range rows {
		b := byEngine[row.Engine]
		if b == nil {
			b = &bucket{settings: map[string]string{}}
			byEngine[row.Engine] = b
		}
		b.settings[row.Param] = row.Value
		if row.AppliedAt == nil {
			b.pending = true
		}
	}
	for engine, b := range byEngine {
		if !b.pending || len(b.settings) == 0 {
			continue // already converged
		}
		cmd := "db.config.apply"
		if engine == "postgres" {
			cmd = "db.postgres.config.apply"
		}
		callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, aerr := r.agent.Call(callCtx, cmd, map[string]any{
			"settings":         b.settings,
			"restart_required": dbtuning.RestartRequired(engine, b.settings),
		})
		cancel()
		if aerr != nil {
			r.log.WarnContext(ctx, "reconcile db tuning: agent apply failed",
				"engine", engine, "err", aerr)
			continue
		}
		if err := r.dbAdmin.MarkTuningApplied(ctx, engine, "", time.Now().UTC()); err != nil {
			r.log.WarnContext(ctx, "reconcile db tuning: mark applied failed",
				"engine", engine, "err", err)
		}
	}
}
