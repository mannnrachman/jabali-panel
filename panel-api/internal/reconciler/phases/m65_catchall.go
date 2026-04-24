package phases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// catchallPhase reconciles domain catch-all settings with Stalwart.
// jabali-as-truth: the catchall_target column in domains table is authoritative.
// On each domain reconciliation, this phase converges the Stalwart x:Domain.catchAllAddress
// to match jabali state (either the target email or null if not set).
type catchallPhase struct {
	agent agent.AgentInterface
}

// Name implements Phase.
func (p *catchallPhase) Name() string {
	return "catchall"
}

// ReconcileDomain converges Stalwart's catchAllAddress to match jabali's catchall_target.
func (p *catchallPhase) ReconcileDomain(ctx context.Context, domain *models.Domain, config map[string]interface{}) error {
	if p.agent == nil {
		// Agent not configured in this reconciler context.
		return nil
	}

	// Extract the catchall target (may be nil).
	var targetValue string
	if domain.CatchallTarget != nil {
		targetValue = *domain.CatchallTarget
	}

	// Call agent domain.catchall_set to sync state to Stalwart.
	agentCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	raw, err := p.agent.Call(agentCtx, "domain.catchall_set", map[string]any{
		"domain_id":   domain.ID,
		"domain_name": domain.Name,
		"target":      targetValue,
	})
	if err != nil {
		return fmt.Errorf("agent domain.catchall_set failed: %w", err)
	}

	// Validate response.
	var resp struct {
		Ok     bool   `json:"ok"`
		Target string `json:"target"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("agent domain.catchall_set unmarshal failed: %w", err)
	}

	if !resp.Ok {
		return fmt.Errorf("agent domain.catchall_set returned ok=false")
	}

	slog.Info("reconciled catch-all", "domain", domain.Name, "target", targetValue)
	return nil
}

// ReconcileMailbox is a no-op for catch-all (domain-level feature only).
func (p *catchallPhase) ReconcileMailbox(ctx context.Context, mailbox *models.Mailbox, domain *models.Domain, config map[string]interface{}) error {
	return nil
}

// NewCatchallPhase creates a catch-all reconciliation phase.
func NewCatchallPhase(agent agent.AgentInterface) Phase {
	return &catchallPhase{agent: agent}
}

func init() {
	// Placeholder: actual agent wiring happens in reconciler.go's Init() method.
	// Register a stub here so the phase name is discoverable during tests.
	// The real agent instance is wired via NewCatchallPhase() in reconciler setup.
}
