// M30.2.x — purge the legacy shared restic password file once every
// backup_destinations row has been migrated to per-destination
// password_enc (AES-GCM via sso.key).
//
// Trigger: ReconcileAll calls reconcileResticLegacyPassword every
// pass. Cheap (one DB query, one agent call) so the per-cycle cost
// is fine. Idempotent: once the file is gone the agent's
// files.delete is a no-op on the next pass.
//
// Safety: never deletes when ANY destination still has password_enc
// IS NULL — those destinations would lose their unlock key. Also
// never deletes when there are zero destinations (fresh install).
package reconciler

import "context"

const legacyResticPasswordPath = "/etc/jabali-panel/restic-repo.password"

func (r *Reconciler) reconcileResticLegacyPassword(ctx context.Context) {
	if r.agent == nil || r.backupDestinations == nil {
		return
	}
	dests, err := r.backupDestinations.List(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "restic legacy password reconcile: list destinations failed",
			"err", err)
		return
	}
	if len(dests) == 0 {
		// Fresh install: don't touch the file. install.sh writes it
		// on first boot for backup-foundation bootstrap.
		return
	}
	for _, d := range dests {
		if !d.Enabled {
			continue
		}
		if len(d.PasswordEnc) == 0 {
			// At least one destination still on the shared file.
			// Hold off until the operator finishes rotating.
			return
		}
	}
	if _, err = r.agent.Call(ctx, "files.delete", map[string]any{
		"path": legacyResticPasswordPath,
	}); err != nil {
		r.log.InfoContext(ctx, "restic legacy password purge: agent delete returned err",
			"path", legacyResticPasswordPath, "err", err)
	}
}
