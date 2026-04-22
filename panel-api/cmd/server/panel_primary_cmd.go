package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// newPanelPrimaryCmd mounts `jabali panel-primary …`. Only child is
// `ensure` — idempotently creates/updates the single is_panel_primary=1
// domain row for the panel hostname. Called by install.sh.
//
// See M6.4 / ADR-0048.
func newPanelPrimaryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "panel-primary",
		Short: "Manage the panel's primary mail domain row (ADR-0048)",
	}
	cmd.AddCommand(newPanelPrimaryEnsureCmd())
	return cmd
}

func newPanelPrimaryEnsureCmd() *cobra.Command {
	var hostname string
	cmd := &cobra.Command{
		Use:   "ensure",
		Short: "Ensure a panel-primary domain row exists for the given hostname",
		Long: `Idempotent primitive called by install.sh:

1. If no is_panel_primary=1 row exists, INSERT one with the given
   hostname owned by the first admin user, email_enabled=1.
2. If one exists with the same hostname, no-op.
3. If one exists with a different hostname, UPDATE the name in place
   (hostname-change case per ADR-0048 Decision 3).
4. If no admin user exists yet, log + exit 0 — caller is expected to
   retry after admin bootstrap completes.`,
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initDB(); err != nil {
				return err
			}
			if strings.TrimSpace(hostname) == "" {
				return fmt.Errorf("--hostname is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			return ensurePanelPrimary(ctx, hostname)
		},
	}
	cmd.Flags().StringVar(&hostname, "hostname", "", "panel hostname (e.g. jabali-panel.local)")
	return cmd
}

// ensurePanelPrimary implements the idempotent decision table.
func ensurePanelPrimary(ctx context.Context, hostname string) error {
	domains := domainRepoFromDB()
	users := userRepo()

	// Find the existing panel-primary row, if any.
	existing, err := domains.FindPanelPrimary(ctx)
	switch {
	case err == nil:
		// Row exists. Same hostname → no-op. Different → update name.
		if existing.Name == hostname {
			fmt.Printf("panel-primary domain row already present (id=%s, name=%s)\n", existing.ID, existing.Name)
			return nil
		}
		// Hostname drift — update the existing row's Name column. Use raw
		// SQL because Domain.Update()'s column allowlist doesn't include
		// every field we might need here (it DOES include name, but we're
		// bypassing the UpdatedAt-bump allowlist anyway by going through
		// the db directly — simpler than plumbing a new repo method for
		// a rare case).
		oldName := existing.Name
		if err := sharedDB.WithContext(ctx).
			Model(&models.Domain{}).
			Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"name":       hostname,
				"updated_at": time.Now(),
			}).Error; err != nil {
			return fmt.Errorf("update panel-primary hostname: %w", err)
		}
		fmt.Printf("panel-primary domain row hostname updated: %q -> %q (id=%s)\n", oldName, hostname, existing.ID)
		fmt.Printf("NOTE: old self-zone and DKIM key for %q are orphaned; see runbook for manual cleanup.\n", oldName)
		return nil

	case errors.Is(err, repository.ErrPanelPrimaryNotFound):
		// No row yet — create one.
		// Look up the first admin user to own the row. Paging one at a
		// time is fine — install.sh typically runs against an empty (or
		// single-admin) users table. Using a larger page is defensive
		// in case the first page has no admins.
		adminID, err := findFirstAdminID(ctx, users)
		if err != nil {
			return err
		}
		if adminID == "" {
			// No admin yet — install.sh will retry after admin bootstrap.
			fmt.Println("no admin user exists yet; deferring panel-primary domain creation until admin bootstrap completes")
			return nil
		}

		// INSERT. is_panel_primary=true, email_enabled=true — reconciler
		// picks it up on next tick and provisions DKIM + Stalwart + nginx
		// mail vhost + MX/SPF/DMARC.
		now := time.Now()
		d := &models.Domain{
			ID:             ids.NewULID(),
			UserID:         adminID,
			Name:           hostname,
			DocRoot:        "", // panel-primary has no public_html (mail-only).
			IsEnabled:      true,
			IsPanelPrimary: true,
			EmailEnabled:   true,
			SSLEnabled:     true,
			IndexPriority:  "html_first",
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := domains.Create(ctx, d); err != nil {
			return fmt.Errorf("create panel-primary domain row: %w", err)
		}
		// Double-check the is_panel_primary=1 invariant — Create doesn't
		// call MarkPanelPrimary, and GORM's Select allowlist on Update
		// doesn't include it either. Belt-and-suspenders: ensure at
		// most one =1 by explicitly calling MarkPanelPrimary (idempotent
		// no-op if the row was created with the flag already set, which
		// it was — this clears any stray =1 on OTHER rows).
		if err := domains.MarkPanelPrimary(ctx, d.ID); err != nil {
			return fmt.Errorf("mark panel-primary: %w", err)
		}
		fmt.Printf("panel-primary domain row created (id=%s, name=%s)\n", d.ID, hostname)
		return nil

	default:
		return fmt.Errorf("find panel-primary: %w", err)
	}
}

// findFirstAdminID scans up to 100 users looking for the first admin.
// Returns "" if none found (install.sh retries later).
func findFirstAdminID(ctx context.Context, users repository.UserRepository) (string, error) {
	list, _, err := users.List(ctx, repository.ListOptions{Limit: 100})
	if err != nil {
		return "", fmt.Errorf("list users: %w", err)
	}
	for _, u := range list {
		if u.IsAdmin {
			return u.ID, nil
		}
	}
	return "", nil
}
