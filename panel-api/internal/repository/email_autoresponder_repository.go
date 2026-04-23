package repository

import (
	"context"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// EmailAutoresponderRepository defines data access for autoresponse/vacation messages.
// Stalwart integration: JMAP VacationResponse (RFC 8621 §8).
// Jabali is truth; reconciler converges to Stalwart (ADR-0051).
type EmailAutoresponderRepository interface {
	FindByMailboxID(ctx context.Context, mailboxID string) (*models.EmailAutoresponder, error)
	Update(ctx context.Context, autoresponder *models.EmailAutoresponder) error
	Delete(ctx context.Context, mailboxID string) error
}

// TODO: Implement emailAutoresponderRepo with concrete CRUD methods in Step 5.
