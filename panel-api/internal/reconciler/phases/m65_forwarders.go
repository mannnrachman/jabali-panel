package phases

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// forwardersPhase converges jabali email_forwarders rows → Stalwart.
// Per-mailbox: collects all aliases + externals, issues one forwarder.apply.
type forwardersPhase struct {
	agent      agent.AgentInterface
	forwarders repository.EmailForwarderRepository
}

func NewForwardersPhase(ag agent.AgentInterface, fw repository.EmailForwarderRepository) Phase {
	return &forwardersPhase{agent: ag, forwarders: fw}
}

func (p *forwardersPhase) Name() string { return "forwarders" }

func (p *forwardersPhase) ReconcileDomain(_ context.Context, _ *models.Domain, _ map[string]interface{}) error {
	return nil
}

func (p *forwardersPhase) ReconcileMailbox(ctx context.Context, mb *models.Mailbox, dom *models.Domain, _ map[string]interface{}) error {
	if p.agent == nil || p.forwarders == nil || dom == nil {
		return nil
	}
	rows, _, err := p.forwarders.ListByMailboxID(ctx, mb.ID, repository.ListOptions{Limit: 500})
	if err != nil {
		return fmt.Errorf("forwarders: list %s: %w", mb.ID, err)
	}
	aliases := []map[string]string{}
	externals := []string{}
	for _, f := range rows {
		if !f.Enabled {
			continue
		}
		switch f.Type {
		case "alias":
			if f.LocalPart != nil {
				aliases = append(aliases, map[string]string{"local_part": *f.LocalPart})
			}
		case "external":
			externals = append(externals, f.Target)
		}
	}
	params := map[string]any{
		"mailbox_email": mb.LocalPart + "@" + dom.Name,
		"aliases":       aliases,
		"externals":     externals,
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := p.agent.Call(callCtx, "forwarder.apply", params); err != nil {
		return fmt.Errorf("forwarders: agent push %s: %w", mb.ID, err)
	}
	return nil
}
