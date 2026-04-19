package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage panel users",
	}
	cmd.AddCommand(
		newUserListCmd(),
		newUserCreateCmd(),
		newUserDeleteCmd(),
	)
	return cmd
}

// ---- list ----

func newUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all users (direct DB — M20-safe)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			users, err := listUsersDirect(ctx)
			if err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"users": users,
					"total": len(users),
				})
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tEMAIL\tUSERNAME\tNAME\tROLE\tKRATOS\tCREATED")
			for _, u := range users {
				role := "user"
				if u.IsAdmin {
					role = "admin"
				}
				username := "—"
				if u.Username != nil && *u.Username != "" {
					username = *u.Username
				}
				name := strings.TrimSpace(u.NameFirst + " " + u.NameLast)
				if name == "" {
					name = "—"
				}
				kratos := "—"
				if u.KratosIdentityID != nil && *u.KratosIdentityID != "" {
					kratos = "✓"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					u.ID, u.Email, username, name, role, kratos, u.CreatedAt.Format(time.DateOnly))
			}
			return w.Flush()
		},
	}
}

// ---- create ----

func newUserCreateCmd() *cobra.Command {
	var (
		email     string
		password  string
		nameFirst string
		nameLast  string
		isAdmin   bool
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new user (direct DB + Kratos; bypasses HTTP auth — M20-safe)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 30s timeout covers the worst-case path: Kratos create (loopback,
			// sub-100ms) + agent user.create (adduser + home-dir perms, low
			// seconds). Generous so a cold Kratos doesn't spuriously fail.
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			u, warn, err := createUserDirect(ctx, cliUserInput{
				Email:     email,
				Password:  password,
				NameFirst: nameFirst,
				NameLast:  nameLast,
				IsAdmin:   isAdmin,
			})
			if err != nil {
				return err
			}

			if jsonOutput {
				out := map[string]interface{}{"user": u}
				if warn != "" {
					out["warning"] = warn
				}
				return printJSON(out)
			}
			fmt.Printf("Created user %s (%s)\n", u.ID, u.Email)
			if u.KratosIdentityID != nil {
				fmt.Printf("Kratos identity: %s\n", *u.KratosIdentityID)
			}
			if warn != "" {
				fmt.Fprintf(os.Stderr, "warning: %s\n", warn)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "user email (required)")
	cmd.Flags().StringVar(&password, "password", "", "user password (required, min 10 chars)")
	cmd.Flags().StringVar(&nameFirst, "name-first", "", "first name")
	cmd.Flags().StringVar(&nameLast, "name-last", "", "last name")
	cmd.Flags().BoolVar(&isAdmin, "admin", false, "grant admin role")

	return cmd
}

// ---- delete ----
//
// user edit + user login are intentionally absent. edit is rare + easier
// from the web UI; login was M5b-era (JWT cli_token flow, removed). For
// recovery, use `curl -X POST http://127.0.0.1:4434/admin/recovery/code` —
// runbook documents the full flow.

func newUserDeleteCmd() *cobra.Command {
	var (
		force bool
		purge bool
	)

	cmd := &cobra.Command{
		Use:   "delete <user-id>",
		Short: "Delete a user (direct DB + cascade domains + Kratos identity + OS teardown — M20-safe)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}
			target, err := userRepo().FindByID(ctx, userID)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("user %q not found", userID)
				}
				return fmt.Errorf("lookup user: %w", err)
			}

			if !force {
				msg := fmt.Sprintf("Delete user %s (%s)?", target.ID, target.Email)
				if purge {
					msg += " This will also delete /home/" + strOr(target.Username, "<no-home>") + " and all its data."
				} else {
					msg += " (home directory WILL be preserved; pass --purge to remove it)"
				}
				fmt.Print(msg + " [y/N]: ")
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			if err := deleteUserDirect(ctx, userID, purge); err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(map[string]string{"deleted": userID})
			}
			fmt.Printf("Deleted user %s (%s)\n", target.ID, target.Email)
			if purge {
				fmt.Println("/home directory removed.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the user's /home directory (default: preserve tenant data)")

	return cmd
}

// strOr returns *s when non-nil + non-empty, else fallback. Tiny helper so
// the confirmation prompt doesn't render "<nil>" for admins without a
// username.
func strOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
