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
	// Terminal states have no successors.
	models.MigrationStateDone:      {},
	models.MigrationStateFailed:    {},
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
