package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/phpext"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newPHPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "php",
		Short: "PHP version + extension + per-user pool management",
	}
	cmd.AddCommand(
		newPHPVersionCmd(),
		newPHPExtCmd(),
		newPHPPoolCmd(),
	)
	return cmd
}

func newPHPVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Manage installed PHP versions",
	}
	cmd.AddCommand(
		newPHPVersionListCmd(),
		newPHPVersionInstallCmd(),
		newPHPVersionReloadCmd(),
	)
	return cmd
}

func newPHPVersionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List installed PHP versions",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			raw, err := sharedAgent.Call(ctx, "php.version.list", map[string]any{})
			if err != nil {
				return fmt.Errorf("php.version.list: %w", err)
			}
			var resp struct {
				Versions []string `json:"versions"`
			}
			_ = json.Unmarshal(raw, &resp)
			if jsonOutput {
				return printJSON(resp)
			}
			if len(resp.Versions) == 0 {
				fmt.Println("No PHP versions installed.")
				return nil
			}
			for _, v := range resp.Versions {
				fmt.Println(v)
			}
			return nil
		},
	}
}

func newPHPVersionInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "install <version>",
		Short:   "Install a PHP version (e.g. 8.4) — installs base + required extensions, starts php<v>-fpm",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Minute)
			defer cancel()
			raw, err := sharedAgent.Call(ctx, "php.version.install", map[string]any{"version": args[0]})
			if err != nil {
				return fmt.Errorf("php.version.install: %w", err)
			}
			var resp struct {
				Version    string `json:"version"`
				Installed  bool   `json:"installed"`
				FPMRunning bool   `json:"fpm_running"`
			}
			_ = json.Unmarshal(raw, &resp)
			if jsonOutput {
				return printJSON(resp)
			}
			fmt.Printf("PHP %s installed=%t fpm_running=%t\n", resp.Version, resp.Installed, resp.FPMRunning)
			return nil
		},
	}
}

func newPHPVersionReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "reload <version>",
		Short:   "Reload php<v>-fpm.service (zero-downtime SIGUSR2)",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if _, err := sharedAgent.Call(ctx, "php.version.reload", map[string]any{"version": args[0]}); err != nil {
				return fmt.Errorf("php.version.reload: %w", err)
			}
			if jsonOutput {
				return printJSON(map[string]any{"version": args[0], "reloaded": true})
			}
			fmt.Printf("Reloaded php%s-fpm.\n", args[0])
			return nil
		},
	}
}

func newPHPExtCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ext",
		Short: "Manage PHP extensions (server-wide per PHP version)",
	}
	cmd.AddCommand(
		newPHPExtListCmd(),
		newPHPExtApplyCmd("install", "Install (apt) an extension package"),
		newPHPExtApplyCmd("remove", "Remove (apt) an extension package"),
		newPHPExtApplyCmd("enable", "Enable an installed extension via phpenmod"),
		newPHPExtApplyCmd("disable", "Disable an installed extension via phpdismod"),
	)
	return cmd
}

func newPHPExtListCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List PHP extensions and their installed/enabled state for a version",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			raw, err := sharedAgent.Call(ctx, "php.ext.list", map[string]any{"version": version})
			if err != nil {
				return fmt.Errorf("php.ext.list: %w", err)
			}
			var resp struct {
				Version    string `json:"version"`
				Extensions []struct {
					Name      string `json:"name"`
					Installed bool   `json:"installed"`
					Enabled   bool   `json:"enabled"`
					BuiltIn   bool   `json:"built_in"`
				} `json:"extensions"`
			}
			_ = json.Unmarshal(raw, &resp)
			if jsonOutput {
				return printJSON(resp)
			}
			fmt.Printf("PHP %s extensions:\n", resp.Version)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tINSTALLED\tENABLED\tBUILT_IN")
			for _, e := range resp.Extensions {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, boolYN(e.Installed), boolYN(e.Enabled), boolYN(e.BuiltIn))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "PHP version (e.g. 8.4) (required)")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func newPHPExtApplyCmd(action, short string) *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:     action + " <ext>",
		Short:   short,
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			// Built-ins are compiled into php<v>-common (always loaded).
			// Agent has no mods-available file to enable/disable, so the
			// agent call would fail with a misleading "ini file doesn't
			// exist" error. Reject early with a clear message.
			if action == "enable" || action == "disable" {
				if spec, ok := phpext.Lookup(args[0]); ok && spec.BuiltIn {
					return fmt.Errorf("extension %q is built into PHP — already loaded, cannot %s", args[0], action)
				}
			}
			raw, err := sharedAgent.Call(ctx, "php.ext.apply", map[string]any{
				"version": version,
				"ext":     args[0],
				"action":  action,
			})
			if err != nil {
				return fmt.Errorf("php.ext.apply (%s): %w", action, err)
			}
			var resp struct {
				Version   string `json:"version"`
				Ext       string `json:"ext"`
				Installed bool   `json:"installed"`
				Enabled   bool   `json:"enabled"`
				LastError string `json:"last_error,omitempty"`
			}
			_ = json.Unmarshal(raw, &resp)
			if jsonOutput {
				return printJSON(resp)
			}
			fmt.Printf("php%s %s %s: installed=%t enabled=%t\n", resp.Version, action, resp.Ext, resp.Installed, resp.Enabled)
			if resp.LastError != "" {
				fmt.Printf("  warning: %s\n", resp.LastError)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "PHP version (required)")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}

func newPHPPoolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Per-user PHP-FPM pool",
	}
	cmd.AddCommand(
		newPHPPoolGetCmd(),
		newPHPPoolSetCmd(),
	)
	return cmd
}

func newPHPPoolGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <user>",
		Short:   "Show a user's PHP pool",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			u, err := resolveUser(ctx, args[0])
			if err != nil {
				return err
			}
			pool, err := repository.NewPHPPoolRepository(sharedDB).FindByUserID(ctx, u.ID)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("no pool for user %s", derefStr(u.Username))
				}
				return fmt.Errorf("get pool: %w", err)
			}
			if jsonOutput {
				return printJSON(pool)
			}
			fmt.Printf("Pool ID:     %s\n", pool.ID)
			fmt.Printf("User:        %s (%s)\n", derefStr(u.Username), u.ID)
			fmt.Printf("PHP version: %s\n", pool.PHPVersion)
			return nil
		},
	}
}

func newPHPPoolSetCmd() *cobra.Command {
	var (
		userLookup string
		version    string
	)
	cmd := &cobra.Command{
		Use:     "set",
		Short:   "Set a user's PHP version (reconciler regenerates pool conf)",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			u, err := resolveUser(ctx, userLookup)
			if err != nil {
				return err
			}
			repo := repository.NewPHPPoolRepository(sharedDB)
			pool, err := repo.FindByUserID(ctx, u.ID)
			if err != nil && !errors.Is(err, repository.ErrNotFound) {
				return fmt.Errorf("get pool: %w", err)
			}
			created := false
			if pool == nil {
				pool = &models.PHPPool{
					ID:                        ids.NewULID(),
					UserID:                    u.ID,
					PHPVersion:                version,
					PmMode:                    "ondemand",
					PmMaxChildren:             20,
					ProcessIdleTimeoutSeconds: 60,
					Status:                    "pending",
				}
				if err := repo.Create(ctx, pool); err != nil {
					return fmt.Errorf("create pool: %w", err)
				}
				created = true
			} else {
				pool.PHPVersion = version
				if err := repo.Update(ctx, pool); err != nil {
					return fmt.Errorf("update pool: %w", err)
				}
			}
			if jsonOutput {
				return printJSON(map[string]any{"pool": pool, "created": created})
			}
			verb := "Updated"
			if created {
				verb = "Created"
			}
			fmt.Printf("%s pool for %s with PHP version %s. Reconciler tick (≤60s) generates pool conf + reloads php-fpm.\n", verb, derefStr(u.Username), version)
			return nil
		},
	}
	cmd.Flags().StringVar(&userLookup, "user", "", "user (id|email|username) (required)")
	cmd.Flags().StringVar(&version, "version", "", "PHP version e.g. 8.4 (required)")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("version")
	return cmd
}
