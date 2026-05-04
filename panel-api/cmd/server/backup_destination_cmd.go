package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internalbackup "git.linux-hosting.co.il/shukivaknin/jabali2/internal/backup"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/backupwrapperhelpers"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

const credsDir = "/etc/jabali-panel/restic-remotes"

func backupDestinationRepoFromDB() repository.BackupDestinationRepository {
	return repository.NewBackupDestinationRepository(sharedDB)
}

func newBackupDestinationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "destination",
		Aliases: []string{"dest"},
		Short:   "Manage backup destinations (local, sftp, s3, b2, azure, gcs, rest)",
	}
	cmd.AddCommand(
		newBackupDestinationListCmd(),
		newBackupDestinationGetCmd(),
		newBackupDestinationCreateCmd(),
		newBackupDestinationUpdateCmd(),
		newBackupDestinationDeleteCmd(),
		newBackupDestinationTestCmd(),
	)
	return cmd
}

func newBackupDestinationListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List backup destinations",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			rows, err := backupDestinationRepoFromDB().List(ctx)
			if err != nil {
				return fmt.Errorf("list destinations: %w", err)
			}
			if jsonOutput {
				return printJSON(map[string]any{"destinations": rows, "total": len(rows)})
			}
			if len(rows) == 0 {
				fmt.Println("No backup destinations.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tKIND\tENABLED\tURL")
			for _, d := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Name, d.Kind, boolYN(d.Enabled), d.URL)
			}
			return w.Flush()
		},
	}
}

func newBackupDestinationGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id-or-name>",
		Short:   "Show a backup destination",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			d, err := resolveBackupDestination(ctx, args[0])
			if err != nil {
				return err
			}
			if jsonOutput {
				return printJSON(d)
			}
			fmt.Printf("ID:       %s\n", d.ID)
			fmt.Printf("Name:     %s\n", d.Name)
			fmt.Printf("Kind:     %s\n", d.Kind)
			fmt.Printf("URL:      %s\n", d.URL)
			fmt.Printf("Enabled:  %s\n", boolYN(d.Enabled))
			if d.CredentialsRef != nil {
				fmt.Printf("Creds:    %s\n", *d.CredentialsRef)
			}
			fmt.Printf("Created:  %s\n", d.CreatedAt.Format(time.RFC3339))
			return nil
		},
	}
}

func newBackupDestinationCreateCmd() *cobra.Command {
	var (
		name      string
		kind      string
		url       string
		envKV     []string
		envStdin  bool
		disabled  bool
	)

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a backup destination",
		Long:    "Create a backup destination. For sftp, --url should be 'sftp:user@host:/path'. For s3/b2/etc, supply credentials via --env or --env-stdin (one KEY=VALUE per line).",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if !validDestKind(kind) {
				return fmt.Errorf("invalid --kind %q (allowed: %v)", kind, models.AllBackupDestinationKinds)
			}
			if err := internalbackup.ValidateURLForKind(kind, url); err != nil {
				return err
			}
			if kind == models.BackupDestinationKindLocal {
				if fi, err := os.Stat(url); err != nil {
					return fmt.Errorf("local path %q does not exist or is unreadable: %w (create it with `install -d -o jabali -g jabali -m 0750 %s` first)", url, err, url)
				} else if !fi.IsDir() {
					return fmt.Errorf("local path %q is not a directory", url)
				}
			}
			env, err := collectEnv(envKV, envStdin)
			if err != nil {
				return err
			}
			d := &models.BackupDestination{
				ID:      ids.NewULID(),
				Name:    name,
				Kind:    kind,
				URL:     url,
				Enabled: !disabled,
			}
			if len(env) > 0 {
				if _, err := sharedAgent.Call(ctx, "backup.dest.creds_write", map[string]any{
					"dest_id": d.ID,
					"env":     env,
				}); err != nil {
					return fmt.Errorf("write credentials: %w", err)
				}
				ref := filepath.Join(credsDir, d.ID+".env")
				d.CredentialsRef = &ref
			}
			if err := backupDestinationRepoFromDB().Create(ctx, d); err != nil {
				if errors.Is(err, repository.ErrConflict) {
					if d.CredentialsRef != nil {
						_, _ = sharedAgent.Call(ctx, "backup.dest.creds_delete", map[string]any{"dest_id": d.ID})
					}
					return fmt.Errorf("destination name %q already exists", name)
				}
				return fmt.Errorf("create destination: %w", err)
			}
			if jsonOutput {
				return printJSON(d)
			}
			fmt.Printf("Created destination %s (%s, %s)\n", d.ID, d.Name, d.Kind)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "destination name (required, unique)")
	cmd.Flags().StringVar(&kind, "kind", "", "destination kind: local|sftp|s3|b2|azure|gcs|rest (required)")
	cmd.Flags().StringVar(&url, "url", "", "restic repo URL (required)")
	cmd.Flags().StringArrayVar(&envKV, "env", nil, "credential env: KEY=VALUE (repeatable)")
	cmd.Flags().BoolVar(&envStdin, "env-stdin", false, "read additional KEY=VALUE lines from stdin")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create in disabled state")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newBackupDestinationUpdateCmd() *cobra.Command {
	var (
		name    string
		url     string
		enable  bool
		disable bool
	)
	cmd := &cobra.Command{
		Use:     "update <id-or-name>",
		Short:   "Update a backup destination",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			d, err := resolveBackupDestination(ctx, args[0])
			if err != nil {
				return err
			}
			changed := false
			if cmd.Flags().Changed("name") {
				d.Name = name
				changed = true
			}
			if cmd.Flags().Changed("url") {
				if err := internalbackup.ValidateURLForKind(d.Kind, url); err != nil {
					return err
				}
				if d.Kind == models.BackupDestinationKindLocal {
					if fi, err := os.Stat(url); err != nil {
						return fmt.Errorf("local path %q does not exist: %w", url, err)
					} else if !fi.IsDir() {
						return fmt.Errorf("local path %q is not a directory", url)
					}
				}
				d.URL = url
				changed = true
			}
			if enable {
				d.Enabled = true
				changed = true
			}
			if disable {
				d.Enabled = false
				changed = true
			}
			if !changed {
				return fmt.Errorf("no changes specified")
			}
			if err := backupDestinationRepoFromDB().Update(ctx, d); err != nil {
				return fmt.Errorf("update destination: %w", err)
			}
			if jsonOutput {
				return printJSON(d)
			}
			fmt.Printf("Updated destination %s (%s)\n", d.ID, d.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new name")
	cmd.Flags().StringVar(&url, "url", "", "new restic repo URL (validated against existing kind)")
	cmd.Flags().BoolVar(&enable, "enable", false, "mark destination enabled")
	cmd.Flags().BoolVar(&disable, "disable", false, "mark destination disabled")
	cmd.MarkFlagsMutuallyExclusive("enable", "disable")
	return cmd
}

func newBackupDestinationDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <id-or-name>",
		Short:   "Delete a backup destination",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			d, err := resolveBackupDestination(ctx, args[0])
			if err != nil {
				return err
			}
			if !force {
				fmt.Printf("Delete destination %s (%s)? Schedules referencing it will lose this destination. [y/N]: ", d.ID, d.Name)
				var c string
				fmt.Scanln(&c)
				if c != "y" && c != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}
			if err := backupDestinationRepoFromDB().Delete(ctx, d.ID); err != nil {
				return fmt.Errorf("delete destination: %w", err)
			}
			_, _ = sharedAgent.Call(ctx, "backup.dest.creds_delete", map[string]any{"dest_id": d.ID})
			if jsonOutput {
				return printJSON(map[string]string{"deleted": d.ID})
			}
			fmt.Printf("Deleted destination %s (%s)\n", d.ID, d.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return cmd
}

func newBackupDestinationTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "test <id-or-name>",
		Short:   "Test connectivity (auto-inits restic repo if missing)",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()
			d, err := resolveBackupDestination(ctx, args[0])
			if err != nil {
				return err
			}
			if d.Kind == models.BackupDestinationKindLocal {
				if jsonOutput {
					return printJSON(map[string]any{"status": "ok", "detail": "local destination — no remote test"})
				}
				fmt.Println("Status: ok (local destination — no remote test needed)")
				return nil
			}
			params := map[string]any{
				"url":           d.URL,
				"extra_options": backupwrapperhelpers.ResticOptionsFor(d),
			}
			if d.CredentialsRef != nil {
				params["credentials_ref"] = *d.CredentialsRef
			}
			if d.Kind == models.BackupDestinationKindSFTP {
				if s := d.ExtraOptionsTyped().SFTP; s != nil {
					params["sftp"] = map[string]any{
						"host":     s.Host,
						"user":     s.User,
						"port":     s.Port,
						"path":     s.Path,
						"auth":     s.Auth,
						"key_path": s.KeyPath,
					}
				}
			}
			raw, err := sharedAgent.Call(ctx, "backup.dest.test", params)
			if err != nil {
				return fmt.Errorf("test: %w", err)
			}
			var result struct {
				Status        string `json:"status"`
				StdoutPreview string `json:"stdout_preview,omitempty"`
				Stderr        string `json:"stderr,omitempty"`
				Detail        string `json:"detail,omitempty"`
			}
			_ = json.Unmarshal(raw, &result)
			if jsonOutput {
				return printJSON(result)
			}
			fmt.Printf("Status: %s\n", result.Status)
			if result.Detail != "" {
				fmt.Printf("Detail: %s\n", result.Detail)
			}
			if result.StdoutPreview != "" {
				fmt.Printf("Output: %s\n", strings.TrimSpace(result.StdoutPreview))
			}
			if result.Stderr != "" {
				fmt.Printf("Stderr: %s\n", strings.TrimSpace(result.Stderr))
			}
			return nil
		},
	}
}

func resolveBackupDestination(ctx context.Context, lookup string) (*models.BackupDestination, error) {
	repo := backupDestinationRepoFromDB()
	if d, err := repo.Get(ctx, lookup); err == nil {
		return d, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("lookup by id: %w", err)
	}
	if d, err := repo.GetByName(ctx, lookup); err == nil {
		return d, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, fmt.Errorf("lookup by name: %w", err)
	}
	return nil, fmt.Errorf("destination %q not found", lookup)
}

func validDestKind(k string) bool {
	for _, v := range models.AllBackupDestinationKinds {
		if v == k {
			return true
		}
	}
	return false
}

func collectEnv(kv []string, fromStdin bool) (map[string]string, error) {
	env := make(map[string]string, len(kv))
	for _, item := range kv {
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --env %q (need KEY=VALUE)", item)
		}
		if strings.ContainsAny(v, "\n\r") {
			return nil, fmt.Errorf("env value for %q contains newline", k)
		}
		env[k] = v
	}
	if fromStdin {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok || k == "" {
				return nil, fmt.Errorf("invalid stdin line %q (need KEY=VALUE)", line)
			}
			env[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
	}
	return env, nil
}
