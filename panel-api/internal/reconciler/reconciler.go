package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/dnscompile"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/nginxrules"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/redirects"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Reconciler syncs the database state with the filesystem (nginx configs, php-fpm pools).
// The database is the source of truth; the reconciler makes the filesystem match.
type Reconciler struct {
	domains        repository.DomainRepository
	users          repository.UserRepository
	dnsZones       repository.DNSZoneRepository
	dnsRecords     repository.DNSRecordRepository
	serverSettings repository.ServerSettingsRepository
	agent          agent.AgentInterface
	log            *slog.Logger
	interval       time.Duration
	// queue holds domain IDs to reconcile out-of-band (non-blocking enqueue)
	queue chan string
}

// Config bundles reconciler configuration.
type Config struct {
	Interval time.Duration
	QueueLen int
}

// New creates a new Reconciler.
func New(domains repository.DomainRepository, users repository.UserRepository, agentClient agent.AgentInterface, log *slog.Logger, cfg Config) *Reconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.QueueLen <= 0 {
		cfg.QueueLen = 100
	}
	return &Reconciler{
		domains:  domains,
		users:    users,
		agent:    agentClient,
		log:      log,
		interval: cfg.Interval,
		queue:    make(chan string, cfg.QueueLen),
	}
}

// WithDNSRepos adds DNS repository support to the reconciler.
// Call this before using ReconcileDNSZone.
func (r *Reconciler) WithDNSRepos(dnsZones repository.DNSZoneRepository, dnsRecords repository.DNSRecordRepository, serverSettings repository.ServerSettingsRepository) *Reconciler {
	r.dnsZones = dnsZones
	r.dnsRecords = dnsRecords
	r.serverSettings = serverSettings
	return r
}

// Start blocks until ctx is cancelled, running ReconcileAll every interval
// and draining the out-of-band queue. Must be called once per process.
func (r *Reconciler) Start(ctx context.Context) {
	r.log.Info("reconciler starting", "interval", r.interval)

	// Run once at startup to converge any stale state
	if err := r.ReconcileAll(ctx); err != nil {
		r.log.Error("initial reconcile failed", "err", err)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("reconciler stopping")
			return
		case domainID := <-r.queue:
			if err := r.ReconcileOne(ctx, domainID); err != nil {
				r.log.Error("reconcile one failed", "domain_id", domainID, "err", err)
			}
		case <-ticker.C:
			if err := r.ReconcileAll(ctx); err != nil {
				r.log.Error("periodic reconcile failed", "err", err)
			}
		}
	}
}

// Schedule enqueues a domain ID for out-of-band reconciliation. Non-blocking;
// drops the request if the queue is full.
func (r *Reconciler) Schedule(domainID string) {
	select {
	case r.queue <- domainID:
	default:
		r.log.Warn("reconcile queue full, dropping request", "domain_id", domainID)
	}
}

// ReconcileAll diffs the DB against the agent's filesystem state and converges them.
// Returns an error if the agent list call fails; on per-domain errors, logs and continues.
func (r *Reconciler) ReconcileAll(ctx context.Context) error {
	// Get the list of enabled sites from the agent
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := r.agent.Call(agentCtx, "domain.list", nil)
	if err != nil {
		return fmt.Errorf("agent list failed: %w", err)
	}

	var resp struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("failed to parse agent response: %w", err)
	}

	agentSites := make(map[string]bool)
	for _, site := range resp.Sites {
		agentSites[site] = true
	}

	// Fetch all domains from DB. Repository.List returns (domains, total, err).
	allDomains, _, err := r.domains.List(ctx, 0, 10000)
	if err != nil {
		return fmt.Errorf("failed to list domains: %w", err)
	}

	enabledDomains := make(map[string]*models.Domain)
	disabledDomains := make(map[string]*models.Domain)

	for i := range allDomains {
		d := &allDomains[i]
		if d.IsEnabled {
			enabledDomains[d.Name] = d
		} else {
			disabledDomains[d.Name] = d
		}
	}

	// Convergence:
	// 1. Enabled DB domain NOT in agent set -> call domain.create
	for name, domain := range enabledDomains {
		if !agentSites[name] {
			r.log.Info("reconcile: creating missing domain", "domain", name)
			r.createDomainOnAgent(ctx, domain)
		}
	}

	// 2. Disabled DB domain that IS in agent set -> call domain.create with is_enabled=false
	for name, domain := range disabledDomains {
		if agentSites[name] {
			r.log.Info("reconcile: disabling unwanted domain", "domain", name)
			r.createDomainOnAgent(ctx, domain)
		}
	}

	// Well-known nginx vhosts that ship with the distro — not managed by
	// the panel, not interesting to log.
	knownSystemSites := map[string]bool{
		"default":         true,
		"default-ssl":     true,
		"000-default":     true,
		"000-default-ssl": true,
	}

	// 3. Orphan in agent set (no DB row) -> log warning but don't auto-delete
	for site := range agentSites {
		if knownSystemSites[site] {
			continue
		}
		if _, found := enabledDomains[site]; !found {
			if _, found := disabledDomains[site]; !found {
				r.log.Warn("reconcile: orphan site found in agent, no DB row", "site", site,
					"detail", "manual cleanup may be needed")
			}
		}
	}

	return nil
}

// ReconcileOne converges a single domain ID. If the domain doesn't exist in the DB,
// it is treated as deleted and we call domain.disable on the agent.
func (r *Reconciler) ReconcileOne(ctx context.Context, domainID string) error {
	domain, err := r.domains.FindByID(ctx, domainID)
	if err != nil {
		// Domain not found in DB (e.g., it was deleted). Assume it's supposed to be gone.
		// We don't know the domain name without a DB row, so we can't disable it.
		// This is okay; the next ReconcileAll will catch any orphans.
		r.log.Info("domain not found in DB, treating as deleted", "domain_id", domainID)
		return nil
	}

	// Always call domain.create with is_enabled to converge to desired state.
	// The agent handles both enabled and disabled via the is_enabled parameter.
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r.createDomainOnAgent(agentCtx, domain)

	return nil
}

// ReconcileAllForce forces regeneration of all domains from the database,
// regardless of their current state on the agent. Every domain gets a fresh
// domain.create call to ensure all configurations are up-to-date.
func (r *Reconciler) ReconcileAllForce(ctx context.Context) error {
	allDomains, _, err := r.domains.List(ctx, 0, 10000)
	if err != nil {
		return fmt.Errorf("failed to list domains: %w", err)
	}

	for i := range allDomains {
		d := &allDomains[i]
		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		r.createDomainOnAgent(agentCtx, d)
		cancel()
	}

	return nil
}

// createDomainOnAgent calls the agent to provision a domain.
// Logs errors but doesn't return them so reconciliation can continue.
func (r *Reconciler) createDomainOnAgent(ctx context.Context, domain *models.Domain) {
	user, err := r.users.FindByID(ctx, domain.UserID)
	if err != nil {
		r.log.Error("failed to fetch user for domain", "domain_id", domain.ID, "user_id", domain.UserID, "err", err)
		return
	}

	// Username should always be set for non-admin users hosting domains.
	if user.Username == nil || *user.Username == "" {
		r.log.Error("user has no username for domain", "domain_id", domain.ID, "user_id", domain.UserID)
		return
	}
	username := *user.Username

	params := map[string]any{
		"username":    username,
		"domain":      domain.Name,
		"doc_root":    domain.DocRoot,
		"php_version": "8.3", // TODO: make configurable
		"is_enabled":  domain.IsEnabled,
	}

	if domain.NginxCustomDirectives != nil {
		params["custom_directives"] = *domain.NginxCustomDirectives
	} else {
		params["custom_directives"] = ""
	}

	params["redirect_directives"] = redirects.Compile(domain)
	params["rule_directives"] = nginxrules.Compile(domain)

	params["index_priority"] = domain.IndexPriority

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = r.agent.Call(callCtx, "domain.create", params)
	if err != nil {
		r.log.Error("domain create failed on agent",
			"domain_id", domain.ID,
			"domain", domain.Name,
			"err", err)
	}

	// Reconcile DNS zone if DNS repos are wired
	r.reconcileDNSZone(ctx, domain)
}

// ReconcileDeleted tears down an OS-level domain whose DB row has already
// been removed. Called by the DELETE handler after it deletes the row,
// because once the row is gone ReconcileOne(id) can no longer find it
// and orphan detection in ReconcileAll is intentionally conservative
// (log-only). This is the explicit "yes, actually tear this down" path.
func (r *Reconciler) ReconcileDeleted(ctx context.Context, domainName string) {
	if domainName == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := r.agent.Call(callCtx, "domain.delete", map[string]string{"domain": domainName})
	if err != nil {
		r.log.Warn("domain delete failed on agent",
			"domain", domainName,
			"err", err)
	}

	// Reconcile DNS zone deletion if DNS repos are wired
	r.reconcileDNSZoneDeleted(ctx, domainName)
}

// reconcileDNSZone ensures a domain's DNS zone and records are provisioned
// on the agent. Called during domain reconciliation to push the zone state
// to PowerDNS via the agent.
func (r *Reconciler) reconcileDNSZone(ctx context.Context, domain *models.Domain) {
	if r.dnsZones == nil {
		return // DNS feature not wired — skip
	}

	zone, err := r.dnsZones.FindByDomainID(ctx, domain.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Zone doesn't exist yet — create + bootstrap.
			zone = &models.DNSZone{
				ID:             ids.NewULID(),
				DomainID:       domain.ID,
				Name:           domain.Name,
				RefreshSeconds: 3600,
				RetrySeconds:   600,
				ExpireSeconds:  604800,
				MinimumTTL:     3600,
				IsEnabled:      true,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
			}
			if err := r.dnsZones.Create(ctx, zone); err != nil {
				r.log.Error("create zone failed", "domain", domain.Name, "err", err)
				return
			}
			srv, _ := r.serverSettings.Get(ctx)
			boots := dnscompile.BootstrapRecords(zone.ID, srv, ids.NewULID)
			for i := range boots {
				if err := r.dnsRecords.Create(ctx, &boots[i]); err != nil {
					r.log.Error("bootstrap record failed", "err", err)
					return
				}
			}
			r.log.Info("bootstrapped DNS zone", "zone", zone.Name, "records", len(boots))
		} else {
			r.log.Error("find zone failed", "domain", domain.Name, "err", err)
			return
		}
	}

	if !zone.IsEnabled {
		return
	}

	records, err := r.dnsRecords.ListByZoneID(ctx, zone.ID)
	if err != nil {
		r.log.Error("list records failed", "zone", zone.Name, "err", err)
		return
	}
	srv, _ := r.serverSettings.Get(ctx)
	compiled := dnscompile.Compile(zone, records, srv)

	// Bump serial on push.
	zone.Serial = time.Now().UTC().Unix()
	_ = r.dnsZones.Update(ctx, zone)

	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := r.agent.Call(pushCtx, "dns.zone.upsert", map[string]any{
		"zone":    zone.Name,
		"records": compiled,
	}); err != nil {
		r.log.Error("dns.zone.upsert failed", "zone", zone.Name, "err", err)
	}
}

// reconcileDNSZoneDeleted tears down a DNS zone on the agent after its DB row
// has been deleted. Called by the domain deletion handler.
func (r *Reconciler) reconcileDNSZoneDeleted(ctx context.Context, zoneName string) {
	if r.dnsZones == nil {
		return // DNS feature not wired — skip
	}
	if zoneName == "" {
		return
	}
	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := r.agent.Call(pushCtx, "dns.zone.delete", map[string]string{"zone": zoneName}); err != nil {
		r.log.Warn("dns.zone.delete failed", "zone", zoneName, "err", err)
	}
}

// linuxUserFromEmail derives the Linux username from an email address.
// Takes the part before the @ symbol (e.g., "alice@example.com" -> "alice").
func linuxUserFromEmail(email string) string {
	for i, ch := range email {
		if ch == '@' {
			return email[:i]
		}
	}
	return email
}
