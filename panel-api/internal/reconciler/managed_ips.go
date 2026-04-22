package reconciler

import (
	"context"
	"encoding/json"
	"time"
)

// ReconcileManagedIPs ensures every row in managed_ips with is_bound=TRUE
// is actually present on the kernel. Called from ReconcileAll; a no-op
// when WithManagedIPs wasn't wired (lets older deployments keep working).
//
// Flow:
//  1. Pull every row from managed_ips.
//  2. Shell out to agent `ip.list` and index the response by address.
//  3. For each row where is_bound=TRUE but the address is absent:
//     call ip.bind with one retry (agent already has preflight).
//  4. Any row whose rebind attempts all fail flips to degraded=TRUE so
//     the admin UI can surface it.
//
// Rows with is_bound=FALSE are untouched — those are addresses the
// operator added to the pool but hasn't asked jabali to manage (e.g.
// netplan-bound). The reconciler must not adopt them silently.
func (r *Reconciler) ReconcileManagedIPs(ctx context.Context) {
	if r.managedIPs == nil || r.agent == nil {
		return
	}

	rows, err := r.managedIPs.ListAll(ctx)
	if err != nil {
		r.log.Error("reconcile managed_ips: list", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	raw, err := r.agent.Call(listCtx, "ip.list", nil)
	cancel()
	if err != nil {
		r.log.Warn("reconcile managed_ips: agent ip.list failed (will retry next pass)", "err", err)
		return
	}
	var listResp struct {
		Entries []struct {
			Address   string `json:"address"`
			Family    string `json:"family"`
			Interface string `json:"interface"`
		} `json:"entries"`
	}
	if jerr := json.Unmarshal(raw, &listResp); jerr != nil {
		r.log.Error("reconcile managed_ips: parse ip.list", "err", jerr)
		return
	}
	present := make(map[string]bool, len(listResp.Entries))
	for _, e := range listResp.Entries {
		present[e.Address] = true
	}

	for i := range rows {
		row := &rows[i]
		if !row.IsBound {
			continue
		}
		if present[row.Address] {
			// Still on the kernel. If it was marked degraded, clear
			// the flag — the address is back.
			if row.Degraded {
				row.Degraded = false
				if uerr := r.managedIPs.Update(ctx, row); uerr != nil {
					r.log.Error("reconcile managed_ips: clear degraded", "address", row.Address, "err", uerr)
				}
			}
			continue
		}

		// Missing from kernel. Re-bind, best effort.
		r.log.Info("reconcile managed_ips: rebinding lost address", "address", row.Address, "family", row.Family)
		bindCtx, bindCancel := context.WithTimeout(ctx, 15*time.Second)
		_, bErr := r.agent.Call(bindCtx, "ip.bind", map[string]any{
			"address": row.Address,
		})
		bindCancel()
		if bErr != nil {
			r.log.Warn("reconcile managed_ips: rebind failed, marking degraded",
				"address", row.Address, "err", bErr)
			row.Degraded = true
			if uerr := r.managedIPs.Update(ctx, row); uerr != nil {
				r.log.Error("reconcile managed_ips: mark degraded", "address", row.Address, "err", uerr)
			}
			continue
		}
		// Success — clear any stale degraded flag.
		if row.Degraded {
			row.Degraded = false
			if uerr := r.managedIPs.Update(ctx, row); uerr != nil {
				r.log.Error("reconcile managed_ips: clear degraded after rebind", "address", row.Address, "err", uerr)
			}
		}
	}
}
