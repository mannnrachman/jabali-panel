package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Reconciler syncs the database state with the filesystem (nginx configs, php-fpm pools).
// The database is the source of truth; the reconciler makes the filesystem match.
type Reconciler struct {
	domains  repository.DomainRepository
	users    repository.UserRepository
	agent    agent.AgentInterface
	log      *slog.Logger
	interval time.Duration
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

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err = r.agent.Call(callCtx, "domain.create", params)
	if err != nil {
		r.log.Error("domain create failed on agent",
			"domain_id", domain.ID,
			"domain", domain.Name,
			"err", err)
	}
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
