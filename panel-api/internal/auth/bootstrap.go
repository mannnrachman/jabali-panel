package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// KratosIdentityWriter is the subset of kratosclient.Client that BootstrapAdmin
// needs. Defined here (not in kratosclient) per Go idiom — interfaces live with
// the consumer, not the producer. Tests pass a fake implementation; production
// passes a real *kratosclient.Client.
type KratosIdentityWriter interface {
	CreateIdentityWithPassword(ctx context.Context, traits kratosclient.AdminTraits, passwordHash string) (string, error)
	DeleteIdentity(ctx context.Context, identityID string) error
}

// BootstrapOptions captures the inputs to BootstrapAdmin.
type BootstrapOptions struct {
	Email      string
	Password   string
	BcryptCost int

	// Kratos, when non-nil, makes BootstrapAdmin atomically create a Kratos
	// identity alongside the panel user row. On any failure the created side
	// is rolled back so panel + Kratos can't drift. Pass nil for legacy auth
	// mode — BootstrapAdmin then behaves exactly as it did pre-M20.
	//
	// Matches the API user-create hook in internal/api/users.go (see
	// cfg.kratosEnabled) so boot-time and runtime paths share the same
	// atomic-create invariant — ADR-0003 ("one write path = the API")
	// extends to bootstrap.
	Kratos KratosIdentityWriter
}

// BootstrapResult tells the caller whether an admin was created, skipped,
// or already existed.
type BootstrapResult struct {
	Created          bool   // true if a new admin row was written
	ExistingID       string // set when an admin already existed (not our insert)
	SkippedEmpty     bool   // true when Email/Password are blank — no-op
	KratosIdentityID string // when Kratos != nil and Created == true, the new identity UUID
}

// BootstrapAdmin creates a single admin user when none exists. Callable
// idempotently on every boot:
//
//   - no env vars set           → SkippedEmpty
//   - admin already exists      → ExistingID populated
//   - no admin exists           → Created=true, new row written
//
// Never updates an existing admin's password — explicit by design so a leaked
// env var can't silently overwrite the live operator credential.
//
// When opt.Kratos is non-nil, also creates a Kratos identity atomically:
//   - panel row insert
//   - Kratos identity create (bcrypt passthrough, same hash as the panel row)
//   - panel row update with kratos_identity_id
//
// Any failure rolls back the prior step(s) so the two systems can't drift. If
// Kratos create fails we delete the panel row; if the second panel update
// fails we delete the Kratos identity and the panel row. Best-effort unwind
// — if a rollback itself fails we log so the operator sees the orphan and
// return the original error so the caller sees a clear signal.
func BootstrapAdmin(ctx context.Context, users repository.UserRepository, opt BootstrapOptions) (BootstrapResult, error) {
	if opt.Email == "" || opt.Password == "" {
		return BootstrapResult{SkippedEmpty: true}, nil
	}

	// Heuristic: an admin exists if we can find one by the given email
	// with is_admin=true. For v1 we don't enumerate the full user list —
	// a simple FindByEmail is sufficient and avoids leaking user counts.
	if u, err := users.FindByEmail(ctx, opt.Email); err == nil {
		if u.IsAdmin {
			existing := BootstrapResult{ExistingID: u.ID}
			if u.KratosIdentityID != nil {
				existing.KratosIdentityID = *u.KratosIdentityID
			}
			return existing, nil
		}
		return BootstrapResult{}, fmt.Errorf("auth: user %q exists but is not admin; refusing to upgrade", opt.Email)
	} else if !errors.Is(err, repository.ErrNotFound) {
		return BootstrapResult{}, fmt.Errorf("auth: lookup admin: %w", err)
	}

	hash, err := HashPassword(opt.Password, opt.BcryptCost)
	if err != nil {
		return BootstrapResult{}, err
	}

	now := time.Now().UTC()
	u := &models.User{
		ID:           ids.NewULID(),
		Email:        opt.Email,
		PasswordHash: hash,
		IsAdmin:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := users.Create(ctx, u); err != nil {
		return BootstrapResult{}, fmt.Errorf("auth: create admin: %w", err)
	}

	if opt.Kratos == nil {
		return BootstrapResult{Created: true}, nil
	}

	// Kratos-aware path. Mirrors the compensating transaction in
	// internal/api/users.go so bootstrap + API share one invariant.
	traits := kratosclient.AdminTraits{
		Email:   u.Email,
		IsAdmin: u.IsAdmin,
	}
	identityID, err := opt.Kratos.CreateIdentityWithPassword(ctx, traits, u.PasswordHash)
	if err != nil {
		if delErr := users.Delete(ctx, u.ID); delErr != nil {
			slog.Error("bootstrap: kratos create failed AND panel rollback also failed — orphan panel row",
				"user_id", u.ID, "email", u.Email, "kratos_err", err, "rollback_err", delErr)
		}
		return BootstrapResult{}, fmt.Errorf("auth: create kratos identity: %w", err)
	}

	u.KratosIdentityID = &identityID
	if err := users.Update(ctx, u); err != nil {
		if delErr := opt.Kratos.DeleteIdentity(ctx, identityID); delErr != nil {
			slog.Error("bootstrap: panel update failed AND kratos rollback also failed — orphan identity",
				"user_id", u.ID, "identity_id", identityID, "update_err", err, "rollback_err", delErr)
		}
		if delErr := users.Delete(ctx, u.ID); delErr != nil {
			slog.Error("bootstrap: panel update failed AND panel rollback also failed — orphan row",
				"user_id", u.ID, "update_err", err, "rollback_err", delErr)
		}
		return BootstrapResult{}, fmt.Errorf("auth: link kratos identity: %w", err)
	}

	return BootstrapResult{Created: true, KratosIdentityID: identityID}, nil
}
