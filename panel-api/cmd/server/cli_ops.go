package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Mirrors the HTTP-handler ops in internal/api/{users,domains}.go — list
// reads, single-row updates, delete with cascade — but goes straight to the
// DB so the CLI stays usable without a browser-mode Kratos session cookie
// (which is what /api/v1/* requires). All helpers assume initConfig +
// initDB already ran (returned no error); they'd panic-nil otherwise.
// That's intentional: these are hot-path wrappers, not library code.

// ---------- user ----------

// listUsersDirect returns every user ordered by created_at ASC. Page size is
// 1000 — enough for any single-operator install and matches the pre-M20 CLI.
func listUsersDirect(ctx context.Context) ([]models.User, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	users, _, err := userRepo().List(ctx, repository.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

// deleteUserDirect removes a user (+ cascades to their domains + tears down
// the OS account). Matches the HTTP delete handler's side effects:
//   - refuses to delete the last admin
//   - caller is responsible for the "don't delete yourself" check (CLI has
//     no authenticated caller, so self-lockout is moot — the operator is
//     root and can always recover via DB)
//   - cascade-deletes all domains the user owned (best-effort per-row)
//   - fires agent user.delete so /home/<user> is torn down
//   - if Kratos is on, deletes the linked identity too
//
// purgeHome controls whether the agent removes /home/<user>. Default false
// matches the HTTP query param so tenant data is preserved by default.
func deleteUserDirect(ctx context.Context, userID string, purgeHome bool) error {
	if err := initConfig(); err != nil {
		return err
	}
	if err := initDB(); err != nil {
		return err
	}
	if err := initAgent(); err != nil {
		return err
	}

	users := userRepo()
	target, err := users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("user %q not found", userID)
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if target.IsAdmin {
		n, err := users.CountAdmins(ctx)
		if err != nil {
			return fmt.Errorf("count admins: %w", err)
		}
		if n <= 1 {
			return fmt.Errorf("refusing to delete the last admin (would lock out the panel)")
		}
	}

	// Cascade domains first. Best-effort per-row so one failure doesn't
	// strand a half-deleted user — matches the HTTP handler.
	domains := domainRepoFromDB()
	if owned, _, err := domains.ListByUserID(ctx, userID, repository.ListOptions{Limit: 500}); err == nil {
		for _, d := range owned {
			if err := domains.Delete(ctx, d.ID); err != nil {
				slog.Warn("cli delete: cascade domain failed",
					"user_id", userID, "domain_id", d.ID, "err", err)
			}
		}
	}

	// Kratos identity delete (if linked).
	if target.KratosIdentityID != nil && sharedCfg.Auth.Kratos.PublicURL != "" {
		k := kratosclient.NewClient(sharedCfg.Auth.Kratos.PublicURL, sharedCfg.Auth.Kratos.AdminURL)
		if err := k.DeleteIdentity(ctx, *target.KratosIdentityID); err != nil {
			// Non-fatal: log + continue with panel delete so the operator
			// isn't blocked by a Kratos 500. They can clean orphan
			// identities via `kratos identities delete <id>`.
			slog.Warn("cli delete: kratos identity delete failed",
				"user_id", userID, "identity_id", *target.KratosIdentityID, "err", err)
		}
	}

	// Panel row delete.
	if err := users.Delete(ctx, userID); err != nil {
		return fmt.Errorf("delete panel row: %w", err)
	}

	// OS teardown. Sync here — the CLI should surface agent failures
	// instead of fire-and-forget silence.
	if sharedAgent != nil && target.Username != nil && *target.Username != "" {
		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := sharedAgent.Call(agentCtx, "user.delete", map[string]any{
			"username":    *target.Username,
			"remove_home": purgeHome,
		}); err != nil {
			slog.Warn("cli delete: agent user.delete failed — panel row gone, OS user remains",
				"user_id", userID, "username", *target.Username, "err", err)
			return fmt.Errorf("panel row deleted but OS teardown failed: %w (rerun with --purge=false to leave home intact, or manually delete user %q)",
				err, *target.Username)
		}
	}
	return nil
}

// ---------- domain ----------

func listDomainsDirect(ctx context.Context) ([]models.Domain, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	domains, _, err := domainRepoFromDB().List(ctx, repository.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	return domains, nil
}

// setDomainEnabledDirect flips the is_enabled column. The reconciler picks
// up the change on its next tick and either materialises or tears down the
// nginx vhost. Returns the updated domain so the caller can confirm.
func setDomainEnabledDirect(ctx context.Context, domainID string, enabled bool) (*models.Domain, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	domains := domainRepoFromDB()
	d, err := domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("domain %q not found", domainID)
		}
		return nil, fmt.Errorf("lookup domain: %w", err)
	}
	d.IsEnabled = enabled
	d.UpdatedAt = time.Now().UTC()
	if err := domains.Update(ctx, d); err != nil {
		return nil, fmt.Errorf("update domain: %w", err)
	}
	return d, nil
}

// deleteDomainDirect removes a domain row. The reconciler detects the
// missing row on its next tick and tears down the nginx vhost + SSL cert
// backlog. No inline nginx call — matches the HTTP handler.
func deleteDomainDirect(ctx context.Context, domainID string) (*models.Domain, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	domains := domainRepoFromDB()
	d, err := domains.FindByID(ctx, domainID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("domain %q not found", domainID)
		}
		return nil, fmt.Errorf("lookup domain: %w", err)
	}
	if err := domains.Delete(ctx, domainID); err != nil {
		return nil, fmt.Errorf("delete domain row: %w", err)
	}
	return d, nil
}
