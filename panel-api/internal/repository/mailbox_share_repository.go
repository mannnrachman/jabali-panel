package repository

import (
	"context"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MailboxShareRepository defines data access for mailbox ACL sharing relationships.
// Stalwart integration: JMAP Mailbox/set + shareWith patch.
// Jabali is truth; reconciler converges to Stalwart (ADR-0051).
type MailboxShareRepository interface {
	FindByID(ctx context.Context, id string) (*models.MailboxShare, error)
	FindByOwnerID(ctx context.Context, ownerMailboxID string, opts ListOptions) ([]models.MailboxShare, int64, error)
	FindBySharedWithID(ctx context.Context, sharedWithMailboxID string, opts ListOptions) ([]models.MailboxShare, int64, error)
	Create(ctx context.Context, share *models.MailboxShare) error
	Update(ctx context.Context, share *models.MailboxShare) error
	Delete(ctx context.Context, id string) error
}

// TODO: Implement mailboxShareRepo with concrete CRUD methods in Step 5.
