package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// BootstrapOptions captures the inputs to BootstrapAdmin.
type BootstrapOptions struct {
	Email      string
	Password   string
	BcryptCost int
}

// BootstrapResult tells the caller whether an admin was created, skipped,
// or already existed.
type BootstrapResult struct {
	Created      bool   // true if a new admin row was written
	ExistingID   string // set when an admin already existed (not our insert)
	SkippedEmpty bool   // true when Email/Password are blank — no-op
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
func BootstrapAdmin(ctx context.Context, users repository.UserRepository, opt BootstrapOptions) (BootstrapResult, error) {
	if opt.Email == "" || opt.Password == "" {
		return BootstrapResult{SkippedEmpty: true}, nil
	}

	// Heuristic: an admin exists if we can find one by the given email
	// with is_admin=true. For v1 we don't enumerate the full user list —
	// a simple FindByEmail is sufficient and avoids leaking user counts.
	if u, err := users.FindByEmail(ctx, opt.Email); err == nil {
		if u.IsAdmin {
			return BootstrapResult{ExistingID: u.ID}, nil
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
	return BootstrapResult{Created: true}, nil
}
