package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
)

// `jabali app *` is a thin direct-DB CLI for the M19 application
// installer surface — mirrors the user/domain pattern in cli_ops.go
// rather than going through HTTP (which 401s under auth.provider="kratos").
//
// Scope deliberately omits `create` and `e2e`: the HTTP create handler
// in api.applications.go pulls in 16 per-app kicker goroutines and
// provisionDBChain — all package-private. Adding a CLI create requires
// extracting that pipeline into a shared service package; tracked
// separately. Operators can still install apps from the UI.
func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Manage one-click app installs (direct DB — M20-safe)",
	}
	cmd.AddCommand(
		newAppRegistryCmd(),
		newAppListCmd(),
		newAppGetCmd(),
		newAppDeleteCmd(),
	)
	return cmd
}

// ---- registry ----

func newAppRegistryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "registry",
		Short: "List available app types and their parameter schemas (direct read — no DB)",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := listAppRegistry()
			if err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"apps":  entries,
					"total": len(entries),
				})
			}

			if len(entries) == 0 {
				fmt.Println("No apps registered")
				return nil
			}

			// Sort by name so the table is stable across runs (registry
			// iteration order is map-random).
			sorted := append([]apps.App(nil), entries...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tDISPLAY\tDB\tPARAMS")
			for _, e := range sorted {
				dbCol := "-"
				if e.RequiresDB {
					dbCol = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, e.DisplayName, dbCol, paramSummary(e.InstallParamSchema))
			}
			return w.Flush()
		},
	}
}

// paramSummary renders the param schema as `name(type[!])` joined by
// ", " so the registry table fits on one line. `!` flags required.
// Missing schemas (RequiresDB-less apps) are rendered as "-" rather
// than blank for readability.
func paramSummary(schema map[string]apps.ParamSpec) string {
	if len(schema) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		spec := schema[k]
		flag := ""
		if spec.Required {
			flag = "!"
		}
		parts = append(parts, fmt.Sprintf("%s(%s%s)", k, spec.Type, flag))
	}
	return strings.Join(parts, ", ")
}

// ---- list ----

func newAppListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed apps (direct DB — M20-safe)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			installs, err := listAppsDirect(ctx)
			if err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"installs": installs,
					"total":    len(installs),
				})
			}

			if len(installs) == 0 {
				fmt.Println("No apps installed")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tAPP\tDOMAIN_ID\tSUBDIR\tSTATUS\tCREATED")
			for _, inst := range installs {
				subdir := inst.Subdirectory
				if subdir == "" {
					subdir = "/"
				}
				appType := inst.AppType
				if appType == "" {
					appType = "wordpress"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					inst.ID, appType, inst.DomainID, subdir, inst.Status,
					inst.CreatedAt.Format(time.RFC3339))
			}
			return w.Flush()
		},
	}
}

// ---- get ----

func newAppGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <install-id>",
		Short: "Show one installed app (direct DB — M20-safe)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			install, err := getAppDirect(ctx, args[0])
			if err != nil {
				return err
			}

			if jsonOutput {
				return printJSON(install)
			}

			subdir := install.Subdirectory
			if subdir == "" {
				subdir = "/"
			}
			appType := install.AppType
			if appType == "" {
				appType = "wordpress"
			}
			fmt.Printf("ID:        %s\n", install.ID)
			fmt.Printf("App:       %s\n", appType)
			fmt.Printf("Domain:    %s\n", install.DomainID)
			fmt.Printf("Subdir:    %s\n", subdir)
			fmt.Printf("Admin:     %s <%s>\n", install.AdminUsername, install.AdminEmail)
			fmt.Printf("Status:    %s\n", install.Status)
			if install.LastError != "" {
				fmt.Printf("LastError: %s\n", install.LastError)
			}
			fmt.Printf("Created:   %s\n", install.CreatedAt.Format(time.RFC3339))
			return nil
		},
	}
}

// ---- delete ----

func newAppDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <install-id>",
		Short: "Delete an installed app (direct DB + agent teardown — M20-safe)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Wider timeout than read commands because the agent has to
			// rm the docroot, drop the database, and restore the nginx
			// placeholder — all serial. Matches the HTTP path's 5-min
			// internal context.
			ctx, cancel := context.WithTimeout(cmd.Context(), 6*time.Minute)
			defer cancel()

			if !force {
				preview, err := getAppDirect(ctx, id)
				if err != nil {
					return fmt.Errorf("fetch application: %w", err)
				}
				appType := preview.AppType
				if appType == "" {
					appType = "wordpress"
				}
				fmt.Printf("Delete %s install %s on domain %s? (type 'yes' to confirm): ",
					appType, preview.ID, preview.DomainID)
				var confirm string
				fmt.Scanln(&confirm)
				if !strings.EqualFold(confirm, "yes") {
					fmt.Println("Cancelled")
					return nil
				}
			}

			install, err := deleteAppDirect(ctx, id)
			if err != nil {
				return err
			}
			fmt.Printf("Application %s deleted (app=%s, domain=%s)\n",
				install.ID, install.AppType, install.DomainID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}
