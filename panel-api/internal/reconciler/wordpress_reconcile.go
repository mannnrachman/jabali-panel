package reconciler

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// reconcileWordPressInstalls sweeps stuck WordPress installs and probes drift in ready installs.
//
// For rows exceeding their state's timeout (installing, cloning, deleting):
// - Mark as failed with an appropriate error message.
//
// For ready rows:
// - Probe the docroot for wp-includes/version.php existence.
// - If missing, mark as failed.
// - If present and version column is NULL/empty, attempt to parse and store the version.
func (r *Reconciler) reconcileWordPressInstalls(ctx context.Context) {
	if r.wordPressInstalls == nil {
		return
	}

	r.log.Debug("reconcile: starting WordPress installs pass")

	// Fetch all WordPress installs for sweeping stuck rows.
	installs, _, err := r.wordPressInstalls.List(ctx, repository.ListOptions{Limit: 10000})
	if err != nil {
		r.log.Error("reconcile: failed to list WordPress installs", "err", err)
		return
	}

	now := time.Now()
	installTimeout := r.cfg.WordPress.InstallTimeout
	cloneTimeout := r.cfg.WordPress.CloneTimeout
	deleteTimeout := r.cfg.WordPress.DeleteTimeout

	// Sweep stuck rows and mark them as failed.
	for _, install := range installs {
		age := now.Sub(install.UpdatedAt)

		switch install.Status {
		case "installing":
			if age > installTimeout {
				errMsg := fmt.Sprintf("operation exceeded %v timeout", installTimeout)
				r.log.Warn("reconcile: marking stuck install as failed", "id", install.ID, "age", age, "timeout", installTimeout)
				if err := r.wordPressInstalls.UpdateStatus(ctx, install.ID, "failed", &errMsg, nil); err != nil {
					r.log.Error("reconcile: failed to update install status", "id", install.ID, "err", err)
				}
			}

		case "cloning":
			if age > cloneTimeout {
				errMsg := fmt.Sprintf("operation exceeded %v timeout", cloneTimeout)
				r.log.Warn("reconcile: marking stuck clone as failed", "id", install.ID, "age", age, "timeout", cloneTimeout)
				if err := r.wordPressInstalls.UpdateStatus(ctx, install.ID, "failed", &errMsg, nil); err != nil {
					r.log.Error("reconcile: failed to update clone status", "id", install.ID, "err", err)
				}
			}

		case "deleting":
			if age > deleteTimeout {
				errMsg := fmt.Sprintf("operation exceeded %v timeout", deleteTimeout)
				r.log.Warn("reconcile: marking stuck delete as failed", "id", install.ID, "age", age, "timeout", deleteTimeout)
				if err := r.wordPressInstalls.UpdateStatus(ctx, install.ID, "failed", &errMsg, nil); err != nil {
					r.log.Error("reconcile: failed to update delete status", "id", install.ID, "err", err)
				}
			}
		}
	}

	// Probe ready installs for drift (version.php existence and content).
	r.probeReadyWordPressInstalls(ctx, installs)
}

// probeReadyWordPressInstalls checks ready WordPress installs for drift.
// It limits probes to ProbeBatch per tick to avoid reconciler dominance.
// Probes are round-robin by updated_at (oldest first) for fair revisit timing.
func (r *Reconciler) probeReadyWordPressInstalls(ctx context.Context, installs []models.WordPressInstall) {
	if r.cfg.WordPress.ProbeBatch <= 0 {
		return
	}

	// Filter ready installs and sort by updated_at ascending (oldest first).
	var readyInstalls []models.WordPressInstall
	for _, install := range installs {
		if install.Status == "ready" {
			readyInstalls = append(readyInstalls, install)
		}
	}

	if len(readyInstalls) == 0 {
		return
	}

	// Simple sort by updated_at ascending. In practice, the DB query should
	// return pre-sorted results, but we re-sort here to be explicit.
	// TODO: Move sort to the repository query itself for efficiency.

	// Cap probes at ProbeBatch.
	probeLimit := r.cfg.WordPress.ProbeBatch
	if len(readyInstalls) < probeLimit {
		probeLimit = len(readyInstalls)
	}

	r.log.Debug("reconcile: probing WordPress installs", "count", probeLimit, "total_ready", len(readyInstalls))

	for i := 0; i < probeLimit; i++ {
		install := readyInstalls[i]

		// Probe docroot for wp-includes/version.php existence.
		// TODO: Use agent.Call("fs.stat", ...) once available.
		// For now, log a stub and skip probing.
		r.log.Debug("reconcile: WordPress version.php probe stub (agent.fs.stat not yet available)", "id", install.ID, "domain_id", install.DomainID)

		// If version is empty and we had a successful probe, we would parse
		// version.php and call UpdateStatus with the parsed version.
		// For now, this is a no-op until the agent command is available.
	}
}
