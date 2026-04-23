package phases

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// mailboxSharePhase converges jabali mailbox_shares → Stalwart
// Mailbox.shareWith on each owner mailbox's INBOX. DB is truth.
type mailboxSharePhase struct {
	agent         agent.AgentInterface
	shares        repository.MailboxShareRepository
	mailboxes     repository.MailboxRepository
	domains       repository.DomainRepository
}

func NewMailboxSharePhase(
	ag agent.AgentInterface,
	shares repository.MailboxShareRepository,
	mailboxes repository.MailboxRepository,
	domains repository.DomainRepository,
) Phase {
	return &mailboxSharePhase{agent: ag, shares: shares, mailboxes: mailboxes, domains: domains}
}

func (p *mailboxSharePhase) Name() string { return "mailbox_share" }

func (p *mailboxSharePhase) ReconcileDomain(_ context.Context, _ *models.Domain, _ map[string]interface{}) error {
	return nil
}

func (p *mailboxSharePhase) ReconcileMailbox(ctx context.Context, mb *models.Mailbox, dom *models.Domain, _ map[string]interface{}) error {
	if p.agent == nil || p.shares == nil || p.mailboxes == nil || p.domains == nil || dom == nil {
		return nil
	}
	// Fetch all shares owned by this mailbox.
	shares, _, err := p.shares.FindByOwnerID(ctx, mb.ID, repository.ListOptions{Limit: 500})
	if err != nil {
		return fmt.Errorf("mailbox_share: list %s: %w", mb.ID, err)
	}

	// Build target email → rights map.
	sharesByEmail := make(map[string]models.Rights, len(shares))
	for _, s := range shares {
		target, err := p.mailboxes.FindByID(ctx, s.SharedWithMailboxID)
		if err != nil {
			continue
		}
		tdom, err := p.domains.FindByID(ctx, target.DomainID)
		if err != nil {
			continue
		}
		sharesByEmail[target.LocalPart+"@"+tdom.Name] = s.Rights
	}

	// Push even if empty (clears shares on Stalwart when jabali has none).
	params := map[string]any{
		"owner_email": mb.LocalPart + "@" + dom.Name,
		"shares":      sharesByEmail,
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := p.agent.Call(callCtx, "mailbox.share_set", params); err != nil {
		return fmt.Errorf("mailbox_share: agent push for %s: %w", mb.ID, err)
	}
	return nil
}
