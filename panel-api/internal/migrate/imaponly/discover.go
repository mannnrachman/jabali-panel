// Package imaponly is a stub Discoverer for the imap_only source
// kind (M35 Step 7). Doesn't connect via SSH — IMAP-only migration
// is mail-only by design + uses imapsync as the workhorse.
//
// Today this file ships only registry registration so the admin UI
// can list 'imap_only' as a known source kind (drawer dropdown).
// Connect / ListAccounts / DescribeAccount return an error directing
// the operator to the imapsync flow which lives separately.
//
// Future commit ships an agent command `migration.imapsync` that
// invokes imapsync with the source + destination credentials and
// records progress in migration_stages — at which point Connect/
// ListAccounts get real implementations.
package imaponly

import (
	"context"
	"errors"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

type Discoverer struct{}

var _ migrate.Discoverer = (*Discoverer)(nil)

func New() *Discoverer { return &Discoverer{} }

var errImapNotWired = errors.New(
	"imap_only migration not wired in v1 — operator path: " +
		"create destination jabali user via /admin/users, then " +
		"run imapsync directly against source IMAP + destination " +
		"loopback IMAP (Stalwart on 127.0.0.1:993). M35 Step 7 " +
		"agent.command + admin REST integration land in a " +
		"follow-up commit.",
)

func (d *Discoverer) Connect(_ context.Context, _ string, _ string, _ migrate.SecretRef) (migrate.Session, error) {
	return nil, errImapNotWired
}

func (d *Discoverer) ListAccounts(_ context.Context, _ migrate.Session) ([]migrate.AccountSummary, error) {
	return nil, errImapNotWired
}

func (d *Discoverer) DescribeAccount(_ context.Context, _ migrate.Session, _ string) (*migrate.AccountManifest, error) {
	return nil, errImapNotWired
}

func (d *Discoverer) Close(_ context.Context, _ migrate.Session) error {
	return nil
}

// stubSession satisfies migrate.Session in the unlikely event a
// caller tries to dispatch on a kind() result. Never produced —
// Connect always errors before Session is built — but typed for
// completeness.
type stubSession struct{}

func (stubSession) Kind() string { return models.MigrationSourceIMAPOnly }
