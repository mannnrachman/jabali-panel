package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Suppress unused import lint — repository is used via userRepo() in root.go.


func requireDB(cmd *cobra.Command, args []string) error {
	if err := initConfig(); err != nil {
		return err
	}
	return initDB()
}

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage panel users",
	}
	cmd.AddCommand(
		newUserListCmd(),
		newUserCreateCmd(),
		newUserEditCmd(),
		newUserDeleteCmd(),
	)
	return cmd
}

// ---- list ----

func newUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all users",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			users, _, err := userRepo().List(ctx, 0, 1000)
			if err != nil {
				return fmt.Errorf("list users: %w", err)
			}

			if jsonOutput {
				return printJSON(users)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tEMAIL\tNAME\tROLE\tCREATED")
			for _, u := range users {
				role := "user"
				if u.IsAdmin {
					role = "admin"
				}
				name := strings.TrimSpace(u.NameFirst + " " + u.NameLast)
				if name == "" {
					name = "—"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					u.ID, u.Email, name, role, u.CreatedAt.Format(time.DateOnly))
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
		Use:     "create",
		Short:   "Create a new user",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" || password == "" {
				return fmt.Errorf("--email and --password are required")
			}
			if len(password) < 10 {
				return fmt.Errorf("password must be at least 10 characters")
			}

			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("hash password: %w", err)
			}

			now := time.Now().UTC()
			u := &models.User{
				ID:           ids.NewULID(),
				Email:        email,
				PasswordHash: string(hash),
				NameFirst:    nameFirst,
				NameLast:     nameLast,
				IsAdmin:      isAdmin,
				CreatedAt:    now,
				UpdatedAt:    now,
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			if err := userRepo().Create(ctx, u); err != nil {
				return fmt.Errorf("create user: %w", err)
			}

			if jsonOutput {
				return printJSON(u)
			}
			fmt.Printf("Created user %s (%s)\n", u.ID, u.Email)
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

// ---- edit ----

func newUserEditCmd() *cobra.Command {
	var (
		email     string
		password  string
		nameFirst string
		nameLast  string
		setAdmin  string // "true" / "false" / "" (unchanged)
	)

	cmd := &cobra.Command{
		Use:     "edit <user-id>",
		Short:   "Edit an existing user",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			repo := userRepo()
			u, err := repo.FindByID(ctx, userID)
			if err != nil {
				return fmt.Errorf("find user: %w", err)
			}

			changed := false
			if email != "" {
				u.Email = email
				changed = true
			}
			if cmd.Flags().Changed("name-first") {
				u.NameFirst = nameFirst
				changed = true
			}
			if cmd.Flags().Changed("name-last") {
				u.NameLast = nameLast
				changed = true
			}
			if setAdmin == "true" {
				u.IsAdmin = true
				changed = true
			} else if setAdmin == "false" {
				u.IsAdmin = false
				changed = true
			}
			if password != "" {
				if len(password) < 10 {
					return fmt.Errorf("password must be at least 10 characters")
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
				if err != nil {
					return fmt.Errorf("hash password: %w", err)
				}
				u.PasswordHash = string(hash)
				changed = true
			}

			if !changed {
				return fmt.Errorf("no changes specified")
			}

			u.UpdatedAt = time.Now().UTC()
			if err := repo.Update(ctx, u); err != nil {
				return fmt.Errorf("update user: %w", err)
			}

			if jsonOutput {
				return printJSON(u)
			}
			fmt.Printf("Updated user %s (%s)\n", u.ID, u.Email)
			return nil
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "new email")
	cmd.Flags().StringVar(&password, "password", "", "new password (min 10 chars)")
	cmd.Flags().StringVar(&nameFirst, "name-first", "", "first name")
	cmd.Flags().StringVar(&nameLast, "name-last", "", "last name")
	cmd.Flags().StringVar(&setAdmin, "admin", "", "set admin role (true/false)")

	return cmd
}

// ---- delete ----

func newUserDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <user-id>",
		Short:   "Delete a user",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			repo := userRepo()
			u, err := repo.FindByID(ctx, userID)
			if err != nil {
				return fmt.Errorf("find user: %w", err)
			}

			if !force {
				fmt.Printf("Delete user %s (%s)? [y/N]: ", u.ID, u.Email)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			if err := repo.Delete(ctx, userID); err != nil {
				return fmt.Errorf("delete user: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]string{"deleted": userID})
			}
			fmt.Printf("Deleted user %s (%s)\n", u.ID, u.Email)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")

	return cmd
}
