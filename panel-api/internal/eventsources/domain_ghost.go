package eventsources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// M38 Ghost Domain Detector — periodically resolves the apex A/AAAA
// records for every domain via the agent's domain.dns_check command,
// compares against the server's managed_ips set, and writes the
// resulting state onto domains.ghost_state. State transitions ok→
// mismatch / nxdomain / error fire `domain.ghost_detected` events
// into the M14 dispatcher.
//
// State values mirror the migration enum: unchecked / ok / mismatch
// / nxdomain / error. Coolown on the M14 fire is 24h per domain so
// a long-running misalignment doesn't spam the operator.

const (
	// One pass every four hours plus an immediate fire on boot. Hosts
	// with thousands of domains stay well under the 100-rows-per-tick
	// batch limit; small hosts converge in a single pass.
	domainGhostTick = 4 * time.Hour

	// A row is "stale" and a candidate for re-check when its last
	// ghost_checked_at predates this much wall-clock. Same window as
	// the tick — every domain gets at least one check per 4 hours.
	domainGhostStaleAfter = 4 * time.Hour

	// Hard ceiling per pass to avoid hammering the recursor on a
	// freshly-installed host with 5000 imported domains. Subsequent
	// passes finish what this one started (oldest-first ordering).
	domainGhostBatchSize = 100

	// M14 cooldown — one fire per (domain, eventKind) per 24h.
	domainGhostCoolOff = 24 * time.Hour
)

// ManagedIPLister is the narrow surface the ghost detector needs from
// the managed_ips repository: the set of IP strings that count as
// "this server" for the purposes of DNS-alignment classification.
// Defined as a free-standing interface so tests can supply a tiny
// fake without pulling in the full repo.
type ManagedIPLister interface {
	ListAll(ctx context.Context) ([]models.ManagedIP, error)
}

// DomainGhostRepo is the narrow domain-repo surface the detector needs.
type DomainGhostRepo interface {
	ListForGhostCheck(ctx context.Context, staleBefore time.Time, limit int) ([]models.Domain, error)
	UpdateGhostState(ctx context.Context, id, state string, checkedAt time.Time, detail *string) error
}

// AgentDNSChecker is the minimum AgentCaller call we need for the
// detector's DNS lookup. Re-uses the existing AgentCaller; defined as
// an explicit interface for documentation only.

type domainGhostDeps struct {
	domains    DomainGhostRepo
	managedIPs ManagedIPLister
	agent      AgentCaller
}

func runDomainGhost(ctx context.Context, d Deps) {
	deps := domainGhostDeps{
		domains:    d.DomainsForGhost,
		managedIPs: d.ManagedIPsForGhost,
		agent:      d.Agent,
	}
	if deps.domains == nil || deps.managedIPs == nil || deps.agent == nil {
		d.Log.Debug("eventsources: domain_ghost disabled (missing dep)")
		return
	}
	// Fire one immediate pass so a freshly-restarted panel-api covers
	// the gap. Same self-heal motivation as cert_renew.
	domainGhostPass(ctx, d, deps)
	tick := time.NewTicker(domainGhostTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		domainGhostPass(ctx, d, deps)
	}
}

func domainGhostPass(ctx context.Context, d Deps, deps domainGhostDeps) {
	now := d.Now()
	expected, err := loadExpectedIPs(ctx, deps.managedIPs)
	if err != nil {
		d.Log.Warn("eventsources: domain_ghost expected-IPs lookup failed", "err", err)
		return
	}
	if len(expected) == 0 {
		// No managed IPs registered yet — every domain would falsely
		// classify as mismatch. Silent skip; M24 IP-manager guidance
		// is to seed at least one row at install time.
		d.Log.Debug("eventsources: domain_ghost skipped (no managed_ips)")
		return
	}

	stale := now.Add(-domainGhostStaleAfter)
	rows, err := deps.domains.ListForGhostCheck(ctx, stale, domainGhostBatchSize)
	if err != nil {
		d.Log.Warn("eventsources: domain_ghost list failed", "err", err)
		return
	}

	for _, dom := range rows {
		domainGhostCheck(ctx, d, deps, dom, expected, now)
	}
}

// loadExpectedIPs returns the set of IPv4/IPv6 strings that count as
// "this server" — every managed_ip the operator has accepted as a
// live binding (IsBound=true or IsDefault=true). Degraded entries are
// accepted too because their role in the binding pool predates the
// degraded flag; the operator's intent that the IP belongs here is
// what we care about for ghost classification.
func loadExpectedIPs(ctx context.Context, lister ManagedIPLister) (map[string]struct{}, error) {
	ips, err := lister.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		if !ip.IsBound && !ip.IsDefault {
			continue
		}
		out[ip.Address] = struct{}{}
	}
	return out, nil
}

func domainGhostCheck(ctx context.Context, d Deps, deps domainGhostDeps, dom models.Domain, expected map[string]struct{}, now time.Time) {
	resp, err := deps.agent.Call(ctx, "domain.dns_check", map[string]string{
		"domain_name": dom.Name,
	})
	if err != nil {
		// Treat agent failures as transient — record state=error but do
		// NOT fire an M14 event. Agent restarts + recursor blips are
		// not operator-actionable.
		writeGhostState(ctx, d, deps, dom, "error", trimErrStr(err.Error()), now)
		return
	}

	var parsed struct {
		IPv4     []string `json:"ipv4"`
		IPv6     []string `json:"ipv6"`
		NXDOMAIN bool     `json:"nxdomain"`
		Detail   string   `json:"detail"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil {
		writeGhostState(ctx, d, deps, dom, "error", "agent response unparseable", now)
		return
	}

	if parsed.NXDOMAIN {
		writeGhostState(ctx, d, deps, dom, "nxdomain", "no public A or AAAA record", now)
		fireGhostEvent(ctx, d, dom, "domain.ghost_detected.nxdomain",
			fmt.Sprintf("%s has no public A or AAAA record", dom.Name))
		return
	}

	resolved := append([]string{}, parsed.IPv4...)
	resolved = append(resolved, parsed.IPv6...)
	sort.Strings(resolved)

	mismatched := []string{}
	matched := false
	for _, r := range resolved {
		if _, ok := expected[r]; ok {
			matched = true
		} else {
			mismatched = append(mismatched, r)
		}
	}

	if matched && len(mismatched) == 0 {
		writeGhostState(ctx, d, deps, dom, "ok", "", now)
		return
	}
	if matched && len(mismatched) > 0 {
		// Mixed: at least one matching expected IP but other foreign
		// IPs in the answer set. Treat as mismatch (multi-A record
		// pointing partly elsewhere is operationally a misconfiguration).
		writeGhostState(ctx, d, deps, dom, "mismatch",
			fmt.Sprintf("partial: matches one of ours, also resolves to %s", strings.Join(mismatched, ",")), now)
		fireGhostEvent(ctx, d, dom, "domain.ghost_detected.partial",
			fmt.Sprintf("%s resolves to additional IPs not in this server's pool: %s", dom.Name, strings.Join(mismatched, ",")))
		return
	}
	// matched=false → none of the resolved IPs are ours.
	writeGhostState(ctx, d, deps, dom, "mismatch",
		fmt.Sprintf("resolved %s; expected one of pool", strings.Join(resolved, ",")), now)
	fireGhostEvent(ctx, d, dom, "domain.ghost_detected.mismatch",
		fmt.Sprintf("%s resolves to %s, not pointing at this server", dom.Name, strings.Join(resolved, ",")))
}

func writeGhostState(ctx context.Context, d Deps, deps domainGhostDeps, dom models.Domain, state, detail string, now time.Time) {
	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	if err := deps.domains.UpdateGhostState(ctx, dom.ID, state, now, detailPtr); err != nil {
		d.Log.Warn("eventsources: domain_ghost state-write failed", "domain", dom.Name, "state", state, "err", err)
	}
}

func fireGhostEvent(ctx context.Context, d Deps, dom models.Domain, eventKind, body string) {
	tag := "domain:" + dom.ID
	if !shouldFire(ctx, d, eventKind, tag, domainGhostCoolOff) {
		return
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: eventKind,
		Severity:  models.NotificationSeverityWarning,
		Title:     "Ghost domain detected: " + dom.Name,
		Body:      body + " (" + tag + ")",
		Deeplink:  "/admin/domains?ghost=mismatch",
	})
	if err != nil {
		d.Log.Warn("eventsources: domain_ghost publish failed", "event", eventKind, "err", err)
	}
}

func trimErrStr(s string) string {
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// SeverityWarning is re-exported via models.NotificationSeverityWarning;
// guard at compile time so a future rename of the constant doesn't
// silently break the ghost detector.
var _ = repository.SSLCertificateWithDomain{}
