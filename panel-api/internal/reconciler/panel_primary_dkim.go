// panel_primary_dkim.go — M6.4 (ADR-0048) auto-provisioning of DKIM +
// Stalwart domain + M6 DNS records for the is_panel_primary=1 row.
//
// Why this is a reconciler responsibility: install.sh inserts the
// domain row directly via the `jabali panel-primary ensure` CLI, which
// bypasses the HTTP email-enable handler (and its EnableDomainEmailInline
// helper that usually does this work). The reconciler is our DB-as-truth
// convergence loop — catching any row with email_enabled=1 but no DKIM
// material belongs here. Matches the same pattern the system uses for
// PHP pool binding: the DB declares intent, the reconciler makes it so.
//
// Also fires for any row that lands in that state by any other route
// (raw SQL edit, backup restore, migration), so the behavior is
// scoped to `is_panel_primary=1` intentionally — we don't want to
// surprise operators by auto-enabling email on a freshly-created
// tenant domain they haven't confirmed.

package reconciler

import (
	"context"
	"encoding/json"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/dnscompile"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const panelPrimaryEmailAgentTimeout = 30 * time.Second

// ensurePanelPrimaryDKIM is a reconciler-scoped mirror of the HTTP
// email-enable handler's EnableDomainEmailInline flow. No-op if DKIM
// is already present (idempotent across reconciler ticks).
//
// Sequence on a row that needs provisioning:
//  1. agent call domain.email_enable → Ed25519 DKIM keypair + Stalwart
//     domain add
//  2. UpdateEmailState on the DB row with selector + public key
//  3. Sync the MX/SPF/DKIM/DMARC DNS records into the self-zone
//
// Errors log and return so the next tick retries — email_enabled stays
// true but DkimSelector stays null, so this same code path fires again.
func (r *Reconciler) ensurePanelPrimaryDKIM(ctx context.Context, domain *models.Domain) {
	if domain == nil || !domain.IsPanelPrimary || !domain.EmailEnabled {
		return
	}
	// Idempotent guard: DKIM already provisioned → nothing to do.
	if domain.DkimSelector != nil && *domain.DkimSelector != "" {
		return
	}
	if r.agent == nil {
		r.log.Warn("panel-primary DKIM: agent unconfigured; skipping", "domain", domain.Name)
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, panelPrimaryEmailAgentTimeout)
	defer cancel()
	raw, err := r.agent.Call(agentCtx, "domain.email_enable", map[string]any{
		"domain_id":   domain.ID,
		"domain_name": domain.Name,
	})
	if err != nil {
		r.log.Error("panel-primary DKIM: agent domain.email_enable failed",
			"domain", domain.Name, "err", err)
		return
	}

	var resp struct {
		Ok            bool   `json:"ok"`
		DKIMSelector  string `json:"dkim_selector"`
		DKIMPublicKey string `json:"dkim_public_key"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		r.log.Error("panel-primary DKIM: agent response unmarshal",
			"domain", domain.Name, "err", err)
		return
	}
	if !resp.Ok || resp.DKIMSelector == "" || resp.DKIMPublicKey == "" {
		r.log.Error("panel-primary DKIM: agent returned incomplete response",
			"domain", domain.Name,
			"ok", resp.Ok,
			"selector", resp.DKIMSelector,
			"pubkey_len", len(resp.DKIMPublicKey))
		return
	}

	selector := resp.DKIMSelector
	pubKey := resp.DKIMPublicKey
	now := time.Now().UTC()
	if err := r.domains.UpdateEmailState(ctx, domain.ID, repository.DomainEmailState{
		Enabled:        true,
		DkimSelector:   &selector,
		DkimPublicKey:  &pubKey,
		EmailEnabledAt: &now,
	}); err != nil {
		r.log.Error("panel-primary DKIM: UpdateEmailState failed",
			"domain", domain.Name, "err", err)
		// The agent-side keypair + Stalwart entry already exist; next
		// tick will retry the DB write. No rollback needed — a panel-
		// primary-only re-enable is idempotent on the agent side.
		return
	}

	// Mutate the caller's struct so subsequent per-tick code (e.g.
	// reconcileWebmailVhosts on the same pass) sees the up-to-date
	// state without a reload.
	domain.DkimSelector = &selector
	domain.DkimPublicKey = &pubKey
	domain.EmailEnabledAt = &now

	r.log.Info("panel-primary DKIM: provisioned",
		"domain", domain.Name, "selector", selector)

	// Best-effort DNS sync. The apex zone was already created by
	// bootstrap_pdns_self_zone at install time; syncPanelPrimaryEmailDNS
	// adds MX/SPF/DKIM/DMARC inside that zone. Warnings are logged; DB
	// state is already committed.
	r.syncPanelPrimaryEmailDNS(ctx, domain.ID, selector, pubKey)
}

// syncPanelPrimaryEmailDNS mirrors syncEmailDNSOnEnableInline in the API
// package without the cross-package import.
func (r *Reconciler) syncPanelPrimaryEmailDNS(ctx context.Context, domainID, selector, pubKey string) {
	if r.dnsZones == nil || r.dnsRecords == nil {
		return
	}
	zone, err := r.dnsZones.FindByDomainID(ctx, domainID)
	if err != nil {
		r.log.Warn("panel-primary DKIM: DNS zone lookup failed",
			"domain_id", domainID, "err", err)
		return
	}
	existing, err := r.dnsRecords.ListByZoneID(ctx, zone.ID)
	if err != nil {
		r.log.Warn("panel-primary DKIM: DNS record list failed",
			"zone_id", zone.ID, "err", err)
		return
	}
	intended := dnscompile.BuildEmailRecords(zone.ID, zone.Name, selector, pubKey, ids.NewULID, time.Now().UTC())
	for i := range intended {
		rec := intended[i]
		if hasExistingM6DNSRecord(existing, rec.Name, rec.Type) {
			continue
		}
		if conflictingDNSRecord(existing, rec.Name, rec.Type) {
			r.log.Warn("panel-primary DKIM: DNS record conflict; leaving existing user record in place",
				"zone_id", zone.ID, "name", rec.Name, "type", rec.Type)
			continue
		}
		if err := r.dnsRecords.Create(ctx, &rec); err != nil {
			r.log.Warn("panel-primary DKIM: DNS record create failed",
				"zone_id", zone.ID, "name", rec.Name, "type", rec.Type, "err", err)
		}
	}
}

// hasExistingM6DNSRecord reports whether any previously-inserted M6 record
// already matches the (name, type) tuple. Matches `managed_by = "m6"` so
// non-M6 rows are never considered "already present" — which is what we
// want for conflict detection on the next rule below.
func hasExistingM6DNSRecord(existing []models.DNSRecord, name, recType string) bool {
	for _, r := range existing {
		if r.Name == name && r.Type == recType && r.ManagedBy != nil && *r.ManagedBy == "m6" {
			return true
		}
	}
	return false
}

// conflictingDNSRecord reports whether a non-M6 row is already at
// (name, type) — we leave those alone so the user's custom DNS wins.
func conflictingDNSRecord(existing []models.DNSRecord, name, recType string) bool {
	for _, r := range existing {
		if r.Name == name && r.Type == recType {
			if r.ManagedBy == nil || *r.ManagedBy != "m6" {
				return true
			}
		}
	}
	return false
}
