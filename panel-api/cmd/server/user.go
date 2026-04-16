package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/clientapi"
)

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
		newUserLoginCmd(),
	)
	return cmd
}

// ---- list ----

func newUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all users",
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			resp, err := client.ListUsers(ctx, 1, 1000)
			if err != nil {
				return fmt.Errorf("list users: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"users": resp.Data,
					"total": resp.Total,
				})
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tEMAIL\tNAME\tROLE\tCREATED")
			for _, u := range resp.Data {
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
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" || password == "" {
				return fmt.Errorf("--email and --password are required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.CreateUserRequest{
				Email:     email,
				Password:  password,
				NameFirst: nameFirst,
				NameLast:  nameLast,
				IsAdmin:   isAdmin,
			}

			user, err := client.CreateUser(ctx, req)
			if err != nil {
				return fmt.Errorf("create user: %w", err)
			}

			if jsonOutput {
				return printJSON(user)
			}
			fmt.Printf("Created user %s (%s)\n", user.ID, user.Email)
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
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.UpdateUserRequest{}
			changed := false

			if email != "" {
				req.Email = &email
				changed = true
			}
			if cmd.Flags().Changed("name-first") {
				req.NameFirst = &nameFirst
				changed = true
			}
			if cmd.Flags().Changed("name-last") {
				req.NameLast = &nameLast
				changed = true
			}
			if password != "" {
				req.Password = &password
				changed = true
			}
			if setAdmin == "true" {
				req.IsAdmin = boolPtr(true)
				changed = true
			} else if setAdmin == "false" {
				req.IsAdmin = boolPtr(false)
				changed = true
			}

			if !changed {
				return fmt.Errorf("no changes specified")
			}

			user, err := client.UpdateUser(ctx, userID, req)
			if err != nil {
				return fmt.Errorf("update user: %w", err)
			}

			if jsonOutput {
				return printJSON(user)
			}
			fmt.Printf("Updated user %s (%s)\n", user.ID, user.Email)
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
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			// Fetch user to get email for confirmation
			user, err := client.GetUser(ctx, userID)
			if err != nil {
				return fmt.Errorf("fetch user: %w", err)
			}

			// Confirm deletion unless --force is set
			if !force {
				fmt.Printf("Delete user %s (%s)? [y/N]: ", user.ID, user.Email)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			if err := client.DeleteUser(ctx, userID); err != nil {
				return fmt.Errorf("delete user: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]string{"deleted": userID})
			}
			fmt.Printf("Deleted user %s (%s)\n", user.ID, user.Email)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation prompt")

	return cmd
}
