package repository

import (
	"context"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// EmailForwarderRepository defines data access for email forwarders (aliases + external forwards).
// Stalwart integration: x:UserAccount.aliases + x:SieveUserScript.
// Jabali is truth; reconciler converges to Stalwart (ADR-0051).
type EmailForwarderRepository interface {
	FindByID(ctx context.Context, id string) (*models.EmailForwarder, error)
	ListByDomainID(ctx context.Context, domainID string, opts ListOptions) ([]models.EmailForwarder, int64, error)
	ListByMailboxID(ctx context.Context, mailboxID string, opts ListOptions) ([]models.EmailForwarder, int64, error)
	Create(ctx context.Context, fwd *models.EmailForwarder) error
	Update(ctx context.Context, fwd *models.EmailForwarder) error
	Delete(ctx context.Context, id string) error
}

// TODO: Implement emailForwarderRepo with concrete CRUD methods in Step 5.
