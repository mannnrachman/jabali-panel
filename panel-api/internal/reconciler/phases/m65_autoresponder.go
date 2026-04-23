package phases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// autoresponderPhase converges jabali email_autoresponders → Stalwart
// VacationResponse. DB is truth (ADR-0051); operator changes via Stalwart
// admin are overwritten on next tick.
type autoresponderPhase struct {
	agent          agent.AgentInterface
	autoresponders repository.EmailAutoresponderRepository
}

func NewAutoresponderPhase(ag agent.AgentInterface, ar repository.EmailAutoresponderRepository) Phase {
	return &autoresponderPhase{agent: ag, autoresponders: ar}
}

func (p *autoresponderPhase) Name() string { return "autoresponder" }

func (p *autoresponderPhase) ReconcileDomain(ctx context.Context, _ *models.Domain, _ map[string]interface{}) error {
	return nil // mailbox-scoped feature
}

func (p *autoresponderPhase) ReconcileMailbox(ctx context.Context, mb *models.Mailbox, dom *models.Domain, _ map[string]interface{}) error {
	if p.agent == nil || p.autoresponders == nil || dom == nil {
		return nil
	}
	ar, err := p.autoresponders.FindByMailboxID(ctx, mb.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Nothing to reconcile.
			return nil
		}
		return fmt.Errorf("autoresponder: find %s: %w", mb.ID, err)
	}
	params := map[string]any{
		"mailbox_email": mb.LocalPart + "@" + dom.Name,
		"enabled":       ar.Enabled,
	}
	if ar.FromDate != nil {
		params["from_date"] = ar.FromDate.UTC().Format(time.RFC3339)
	}
	if ar.ToDate != nil {
		params["to_date"] = ar.ToDate.UTC().Format(time.RFC3339)
	}
	if ar.Subject != nil {
		params["subject"] = *ar.Subject
	}
	if ar.TextBody != nil {
		params["text_body"] = *ar.TextBody
	}
	if ar.HTMLBody != nil {
		params["html_body"] = *ar.HTMLBody
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := p.agent.Call(callCtx, "autoresponder.set", params); err != nil {
		return fmt.Errorf("autoresponder: agent push for %s: %w", mb.ID, err)
	}
	return nil
}
