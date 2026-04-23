package phases

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// disclaimerPhase pushes per-domain disclaimer (system sieve script)
// state to Stalwart. DB is truth (ADR-0051 + ADR-0052).
type disclaimerPhase struct {
	agent agent.AgentInterface
}

func NewDisclaimerPhase(ag agent.AgentInterface) Phase {
	return &disclaimerPhase{agent: ag}
}

func (p *disclaimerPhase) Name() string { return "disclaimer" }

func (p *disclaimerPhase) ReconcileDomain(ctx context.Context, dom *models.Domain, _ map[string]interface{}) error {
	if p.agent == nil || dom == nil || !dom.EmailEnabled {
		return nil
	}
	text := ""
	if dom.DisclaimerText != nil {
		text = *dom.DisclaimerText
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := p.agent.Call(callCtx, "domain.disclaimer_apply", map[string]any{
		"domain_name": dom.Name,
		"enabled":     dom.DisclaimerEnabled,
		"text":        text,
	}); err != nil {
		return fmt.Errorf("disclaimer: push %s: %w", dom.Name, err)
	}
	return nil
}

func (p *disclaimerPhase) ReconcileMailbox(_ context.Context, _ *models.Mailbox, _ *models.Domain, _ map[string]interface{}) error {
	return nil
}
