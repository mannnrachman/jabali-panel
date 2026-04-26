package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
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
		newUserPasswordCmd(),
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
		Use:   "delete <email|username|user-id>",
		Short: "Delete a user (direct DB + cascade domains + Kratos identity + OS teardown — M20-safe)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lookup := strings.TrimSpace(args[0])
			if lookup == "" {
				return fmt.Errorf("user identifier is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}
			target, err := resolveUser(ctx, lookup)
			if err != nil {
				return err
			}
			userID := target.ID

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

// ---- password ----
//
// `jabali user password <email|username|user-id>` resets the password on the
// user's Kratos identity. Works for both admins and regular users — the panel
// users table has no separate admin-password path since M20. Safe to run on a
// host with no live user session: this talks to the Kratos admin API directly
// (unix socket via sharedCfg) and doesn't need an HTTP self-service flow.
//
// Password resolution order:
//   --password <pwd>    explicit value (visible in shell history; convenience
//                       for one-shot operator use, mirrors `mailbox create
//                       --password`)
//   --password-stdin    piped password, no prompt — for automation
//   --link              emit a Kratos recovery URL; user picks their own
//   (none of the above) auto-generate a strong password (ULID, 26 chars) and
//                       print it once on stdout — mirrors `mailbox create`
//                       and `mailbox rotate-password` behavior
//
// The legacy TTY twice-prompt was removed — auto-generate is friendlier for
// the common operator case ("reset this user's password and tell me what it
// is") and matches the mailbox CLI affordance the user already knows.

func newUserPasswordCmd() *cobra.Command {
	var (
		password string
		viaStdin bool
		viaLink  bool
		ttl      string
	)

	cmd := &cobra.Command{
		Use:     "password <email|username|user-id>",
		Short:   "Reset a user's password (auto-generates one if --password is omitted)",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			lookup := strings.TrimSpace(args[0])
			if lookup == "" {
				return fmt.Errorf("user identifier is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()

			target, err := resolveUser(ctx, lookup)
			if err != nil {
				return err
			}
			if target.KratosIdentityID == nil || *target.KratosIdentityID == "" {
				return fmt.Errorf("user %s (%s) has no kratos_identity_id — cannot reset password (run `jabali admin rebuild-kratos` to provision)", target.ID, target.Email)
			}

			kratosCfg := sharedCfg.Auth.Kratos
			if kratosCfg.AdminURL == "" {
				return fmt.Errorf("auth.kratos.admin_url not configured — cannot reset password without admin API access")
			}
			kc := kratosclient.NewClient(kratosCfg.PublicURL, kratosCfg.AdminURL)

			if viaLink {
				rc, err := kc.CreateRecoveryCode(ctx, *target.KratosIdentityID, ttl)
				if err != nil {
					return fmt.Errorf("generate recovery link: %w", err)
				}
				if jsonOutput {
					return printJSON(map[string]string{
						"user_id":       target.ID,
						"email":         target.Email,
						"recovery_link": rc.RecoveryLink,
						"recovery_code": rc.RecoveryCode,
						"expires_at":    rc.ExpiresAt,
					})
				}
				fmt.Printf("Recovery link for %s (%s):\n", target.ID, target.Email)
				fmt.Printf("  URL:        %s\n", rc.RecoveryLink)
				fmt.Printf("  Code:       %s\n", rc.RecoveryCode)
				fmt.Printf("  Expires at: %s\n", rc.ExpiresAt)
				fmt.Println("\nSend this link to the user — they'll pick their own password.")
				return nil
			}

			// Resolve the new password. Explicit --password wins; --password-stdin
			// reads one line; otherwise auto-generate. Keep the conflict check
			// strict — silently ignoring one of two contradictory flags hides
			// scripting bugs.
			if password != "" && viaStdin {
				return fmt.Errorf("--password and --password-stdin are mutually exclusive")
			}

			generated := ""
			newPwd := password
			switch {
			case viaStdin:
				p, err := readPasswordStdin()
				if err != nil {
					return err
				}
				newPwd = p
			case newPwd == "":
				newPwd = ids.NewULID()
				generated = newPwd
			}
			if len(newPwd) < 10 {
				return fmt.Errorf("password must be at least 10 characters")
			}

			hash, err := bcrypt.GenerateFromPassword([]byte(newPwd), 12)
			if err != nil {
				return fmt.Errorf("bcrypt hash: %w", err)
			}
			if err := kc.SetPassword(ctx, *target.KratosIdentityID, string(hash)); err != nil {
				return fmt.Errorf("kratos set password: %w", err)
			}

			if jsonOutput {
				out := map[string]string{
					"user_id": target.ID,
					"email":   target.Email,
					"kratos":  *target.KratosIdentityID,
					"status":  "password_reset",
				}
				if generated != "" {
					out["password"] = generated
				}
				return printJSON(out)
			}
			if generated != "" {
				fmt.Printf("Password reset for %s (%s)\n", target.ID, target.Email)
				fmt.Printf("New password: %s\n", generated)
				fmt.Fprintln(os.Stderr, "(shown once — record it now)")
			} else {
				fmt.Printf("Password reset for %s (%s)\n", target.ID, target.Email)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&password, "password", "", "explicit new password (omit to auto-generate)")
	cmd.Flags().BoolVar(&viaStdin, "password-stdin", false, "read new password from stdin (no prompt, no echo)")
	cmd.Flags().BoolVar(&viaLink, "link", false, "emit a one-click recovery URL instead of setting the password directly")
	cmd.Flags().StringVar(&ttl, "expires-in", "24h", "TTL for recovery link (only with --link)")
	return cmd
}

// resolveUser accepts email, username, or user UUID — tries each in the order
// most likely to be unambiguous. Email is primary; username is unique per M20;
// UUID is the ID column lookup. The first hit wins.
func resolveUser(ctx context.Context, lookup string) (*models.User, error) {
	users := userRepo()

	if strings.Contains(lookup, "@") {
		u, err := users.FindByEmail(ctx, lookup)
		if err == nil {
			return u, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("lookup by email: %w", err)
		}
	}
	if u, err := users.FindByUsername(ctx, lookup); err == nil {
		return u, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("lookup by username: %w", err)
	}
	if u, err := users.FindByID(ctx, lookup); err == nil {
		return u, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("lookup by id: %w", err)
	}
	return nil, fmt.Errorf("no user found matching %q", lookup)
}

// readPasswordStdin reads a single line from stdin, stripping the trailing
// newline. Used by the --password-stdin path; explicit --password and
// auto-generate paths bypass this.
func readPasswordStdin() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
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
