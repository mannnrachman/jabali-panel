package migrate

import (
	"errors"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Stage names recorded in migration_stages.stage_name. Lower-case
// snake_case so they're URL-safe in the per-stage logs endpoint
// (Step 8 UI exposes /admin/migrations/:id/stages/:stage/logs).
const (
	StageAnalyze   = "analyze"
	StageFixPerms  = "fix_perms"
	StageValidate  = "validate"
	StageRestore   = "restore"
)

// AllStages is the canonical pipeline order. Importers seed
// migration_stages with one row per name on job creation; resume
// scans in this order and skips any state='done' rows.
var AllStages = []string{
	StageAnalyze,
	StageFixPerms,
	StageValidate,
	StageRestore,
}

// Stage states recorded in migration_stages.state.
const (
	StageStatePending = "pending"
	StageStateRunning = "running"
	StageStateDone    = "done"
	StageStateFailed  = "failed"
)

// ErrIllegalTransition is returned by NextJobState when the caller
// asks for a transition the state machine forbids. Callers should
// surface this as a 409 conflict, not a 500 — it indicates a
// concurrent mutation, not a server bug.
var ErrIllegalTransition = errors.New("illegal migration state transition")

// jobTransitions pins the legal next-states for every job state.
// Restoring → done | failed | cancelled is the most common terminal
// path; pending → cancelled lets operators kill a never-started job
// from the admin UI without spinning up the transient unit first.
var jobTransitions = map[string]map[string]struct{}{
	// Draft is wizard-only — operator can submit (→ pending) or
	// cancel before the row ever reaches the runner. ADR-0095
	// decision 5. delete-instead-of-cancel is also valid via the
	// REST DELETE → destroy path; cancel is here for UI symmetry
	// (same red "Cancel" button across all non-terminal rows).
	models.MigrationStateDraft: {
		models.MigrationStatePending:   {},
		models.MigrationStateCancelled: {},
	},
	models.MigrationStatePending: {
		models.MigrationStateAnalyzing: {},
		models.MigrationStateCancelled: {},
	},
	models.MigrationStateAnalyzing: {
		models.MigrationStateFixPerms:  {},
		models.MigrationStateFailed:    {},
		models.MigrationStateCancelled: {},
	},
	models.MigrationStateFixPerms: {
		models.MigrationStateValidating: {},
		models.MigrationStateFailed:     {},
		models.MigrationStateCancelled:  {},
	},
	models.MigrationStateValidating: {
		models.MigrationStateRestoring: {},
		models.MigrationStateFailed:    {},
		models.MigrationStateCancelled: {},
	},
	models.MigrationStateRestoring: {
		models.MigrationStateDone:      {},
		models.MigrationStateFailed:    {},
		models.MigrationStateCancelled: {},
	},
	// Done + Cancelled are terminal. Failed is RESUMABLE: the
	// operator can rerun `jabali migrate import` after addressing the
	// root cause (e.g. pull-source timeout or SSH flap) so the runner
	// re-enters at analyze. retry-from-scratch wipes stage rows first
	// + flips the row back to pending; this transition covers the
	// gentler resume path that keeps any already-done stages.
	models.MigrationStateDone: {},
	models.MigrationStateFailed: {
		models.MigrationStateAnalyzing: {},
		models.MigrationStateCancelled: {},
	},
	models.MigrationStateCancelled: {},
}

// IsValidJobTransition reports whether `to` is a legal successor of
// `from`. Importers call this before issuing repo.UpdateState so a
// crash mid-run can't half-write an illegal state.
func IsValidJobTransition(from, to string) bool {
	allowed, ok := jobTransitions[from]
	if !ok {
		return false
	}
	_, found := allowed[to]
	return found
}

// StageNameForState returns the migration_stages.stage_name to
// update when a job enters `jobState`. Failed / cancelled / done
// have no per-stage update — caller stamps job-level only.
func StageNameForState(jobState string) string {
	switch jobState {
	case models.MigrationStateAnalyzing:
		return StageAnalyze
	case models.MigrationStateFixPerms:
		return StageFixPerms
	case models.MigrationStateValidating:
		return StageValidate
	case models.MigrationStateRestoring:
		return StageRestore
	default:
		return ""
	}
}
