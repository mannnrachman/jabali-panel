package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/mailaddr"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// mailbox_ops.go mirrors the HTTP mailbox handlers but goes straight to
// the DB + agent the way `cli_ops.go` does for users/domains. The CLI
// bypasses Kratos auth because it runs in the `jabali` group on-box, so
// there's no per-user claims resolution — ownership checks collapse to
// "admin", which is fine for operator-only tooling.
//
// Constants must stay in sync with panel-api/internal/api/mailboxes.go
// (defaultMailboxQuotaBytes, minMailboxQuotaBytes, mailboxBcryptCost,
// mailboxAgentTimeout). We duplicate them here rather than export from
// the api package because the CLI doesn't link against gin.
const (
	cliMailboxDefaultQuotaBytes uint64 = 1 << 30
	cliMailboxMinQuotaBytes     uint64 = 16 * 1024 * 1024
	cliMailboxBcryptCost               = bcrypt.DefaultCost
	cliMailboxAgentTimeout             = 30 * time.Second
)

// agentNotifier is the minimal surface the CLI needs from the agent
// client. Small interface so tests can pass a recording stub without
// pulling in the full *agent.Client. Matches ADR-0013 best-effort
// semantics: errors here don't fail the command.
type agentNotifier func(ctx context.Context, cmd string, params any)

// mailboxRepoFromDB is the CLI-side constructor. Mirrors
// domainRepoFromDB / packageRepoFromDB in root.go.
func mailboxRepoFromDB() repository.MailboxRepository {
	return repository.NewMailboxRepository(sharedDB)
}

// resolveDomainSpec accepts either a domain name (preferred CLI UX) or
// a ULID and returns the Domain row.
//
// The name path is primary: `jabali mailbox create --domain example.com`
// reads better than an opaque ULID. ULID form is handy for scripts
// piping `jabali domain list --json` output.
func resolveDomainSpec(ctx context.Context, domains repository.DomainRepository, spec string) (*models.Domain, error) {
	if spec == "" {
		return nil, fmt.Errorf("domain spec is empty")
	}
	// Try ID first (ULIDs are exactly 26 chars of Crockford base32; bare
	// names never look like that).
	if len(spec) == 26 {
		if d, err := domains.FindByID(ctx, spec); err == nil {
			return d, nil
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("lookup domain by id: %w", err)
		}
	}
	d, err := domains.FindByName(ctx, spec)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("domain %q not found", spec)
		}
		return nil, fmt.Errorf("lookup domain by name: %w", err)
	}
	return d, nil
}

// listMailboxesDirect returns every mailbox in `domainID`. Page size is
// 1000 — matches listDomainsDirect / listUsersDirect. Caller formats.
func listMailboxesDirect(ctx context.Context, repo repository.MailboxRepository, domainID string) ([]models.Mailbox, error) {
	rows, _, err := repo.ListByDomainID(ctx, domainID, repository.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list mailboxes: %w", err)
	}
	return rows, nil
}

// createMailboxDirect mirrors POST /domains/:id/mailboxes:
//   - canonicalise the local part via mailaddr.Canonicalise
//   - enforce ExistsByDomainAndLocalPart uniqueness
//   - generate ULID password if caller didn't pass one
//   - bcrypt the password
//   - Create() then fire mailbox.create agent RPC best-effort (ADR-0013)
//
// Returns the row plus the generated password, which is empty when the
// caller supplied one — the CLI layer owns the reveal-once printing
// contract.
func createMailboxDirect(ctx context.Context, repo repository.MailboxRepository, notify agentNotifier, dom *models.Domain, localPart, password string, quotaBytes uint64) (*models.Mailbox, string, error) {
	if !dom.EmailEnabled {
		return nil, "", fmt.Errorf("email is not enabled on domain %s — run `jabali domain email-enable %s` first", dom.Name, dom.Name)
	}
	canonLocal, _, err := mailaddr.Canonicalise(localPart + "@" + dom.Name)
	if err != nil {
		return nil, "", fmt.Errorf("invalid local_part: %w", err)
	}

	exists, err := repo.ExistsByDomainAndLocalPart(ctx, dom.ID, canonLocal)
	if err != nil {
		return nil, "", fmt.Errorf("uniqueness check: %w", err)
	}
	if exists {
		return nil, "", fmt.Errorf("mailbox %s@%s already exists", canonLocal, dom.Name)
	}

	generated := ""
	if password == "" {
		password = ids.NewULID()
		generated = password
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cliMailboxBcryptCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash password: %w", err)
	}

	if quotaBytes == 0 {
		quotaBytes = cliMailboxDefaultQuotaBytes
	}
	if quotaBytes < cliMailboxMinQuotaBytes {
		return nil, "", fmt.Errorf("quota-mb must be at least 16 (MiB)")
	}

	now := time.Now().UTC()
	mb := &models.Mailbox{
		ID:           ids.NewULID(),
		DomainID:     dom.ID,
		LocalPart:    canonLocal,
		PasswordHash: string(hash),
		QuotaBytes:   quotaBytes,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repo.Create(ctx, mb); err != nil {
		return nil, "", fmt.Errorf("insert mailbox row: %w", err)
	}

	// Best-effort agent notify, same as the HTTP handler. Errors are
	// swallowed inside `notify`; the reconciler re-asserts if Stalwart
	// ever diverges.
	if notify != nil {
		notify(ctx, "mailbox.create", map[string]any{
			"id":    mb.ID,
			"email": canonLocal + "@" + dom.Name,
		})
	}
	// Fill EmailCached locally — the BEFORE INSERT trigger already wrote
	// it in the DB, but our `mb` struct doesn't reflect that without a
	// re-read. Set it so the caller's printout is correct.
	mb.EmailCached = canonLocal + "@" + dom.Name
	return mb, generated, nil
}

// deleteMailboxDirect mirrors DELETE /mailboxes/:mbid: agent call first
// (it owns the RocksDB-side destroy; failure means we bail BEFORE the
// DB delete so Stalwart state matches), then the row.
//
// Unlike create/set-quota/rotate-password, the agent call is a HARD
// dependency here — the Stalwart account must be destroyed first or we
// end up with a tombstoned DB row whose Stalwart side is still valid.
// So this helper takes an agent-call func that returns an error rather
// than the fire-and-forget agentNotifier.
//
// agentCaller's return mirrors agent.Client.Call: raw bytes + error.
// Delete discards the bytes; email-enable unmarshals them for the DKIM
// fields (see domain_email_cmd.go).
type agentCaller func(ctx context.Context, cmd string, params any) (json.RawMessage, error)

func deleteMailboxDirect(ctx context.Context, repo repository.MailboxRepository, call agentCaller, email string) error {
	mb, err := repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("mailbox %s not found", email)
		}
		return fmt.Errorf("lookup mailbox: %w", err)
	}
	if call == nil {
		return fmt.Errorf("agent not configured")
	}
	if _, err := call(ctx, "mailbox.delete", map[string]any{
		"id":    mb.ID,
		"email": email,
	}); err != nil {
		return fmt.Errorf("agent mailbox.delete: %w", err)
	}
	if err := repo.Delete(ctx, mb.ID); err != nil {
		return fmt.Errorf("delete mailbox row: %w", err)
	}
	return nil
}

// setMailboxQuotaDirect mirrors PATCH /mailboxes/:mbid. Quota floor
// check matches the HTTP handler (16 MiB).
func setMailboxQuotaDirect(ctx context.Context, repo repository.MailboxRepository, notify agentNotifier, email string, quotaBytes uint64) (*models.Mailbox, error) {
	if quotaBytes < cliMailboxMinQuotaBytes {
		return nil, fmt.Errorf("quota-mb must be at least 16 (MiB)")
	}
	mb, err := repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("mailbox %s not found", email)
		}
		return nil, fmt.Errorf("lookup mailbox: %w", err)
	}
	if err := repo.UpdateQuota(ctx, mb.ID, quotaBytes); err != nil {
		return nil, fmt.Errorf("update quota: %w", err)
	}
	if notify != nil {
		notify(ctx, "mailbox.set_quota", map[string]any{
			"id":          mb.ID,
			"email":       email,
			"quota_bytes": quotaBytes,
		})
	}
	mb.QuotaBytes = quotaBytes
	mb.UpdatedAt = time.Now().UTC()
	return mb, nil
}

// rotateMailboxPasswordDirect mirrors POST /mailboxes/:mbid/rotate-password.
// Empty `newPassword` → generate a ULID and return it once.
func rotateMailboxPasswordDirect(ctx context.Context, repo repository.MailboxRepository, notify agentNotifier, email, newPassword string) (string, error) {
	mb, err := repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", fmt.Errorf("mailbox %s not found", email)
		}
		return "", fmt.Errorf("lookup mailbox: %w", err)
	}

	generated := ""
	if newPassword == "" {
		newPassword = ids.NewULID()
		generated = newPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), cliMailboxBcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	if err := repo.UpdatePasswordHash(ctx, mb.ID, string(hash)); err != nil {
		return "", fmt.Errorf("update password: %w", err)
	}
	if notify != nil {
		notify(ctx, "mailbox.set_password", map[string]any{
			"id":    mb.ID,
			"email": email,
		})
	}
	return generated, nil
}

// notifyAgentMailbox is the production agentNotifier wired off the
// global sharedAgent. Swallows errors — ADR-0013 best-effort.
func notifyAgentMailbox(ctx context.Context, cmd string, params any) {
	if sharedAgent == nil {
		return
	}
	agentCtx, cancel := context.WithTimeout(ctx, cliMailboxAgentTimeout)
	defer cancel()
	_, _ = sharedAgent.Call(agentCtx, cmd, params)
}

// callAgentMailbox is the production agentCaller — used by delete
// (hard dependency) and email-enable (needs the body). Surfaces the
// error AND the raw payload back up.
func callAgentMailbox(ctx context.Context, cmd string, params any) (json.RawMessage, error) {
	if sharedAgent == nil {
		return nil, fmt.Errorf("agent not configured")
	}
	agentCtx, cancel := context.WithTimeout(ctx, cliMailboxAgentTimeout)
	defer cancel()
	return sharedAgent.Call(agentCtx, cmd, params)
}
