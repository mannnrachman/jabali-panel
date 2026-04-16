package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/clientapi"
)

func newPackageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Manage hosting packages",
	}
	cmd.AddCommand(
		newPackageListCmd(),
		newPackageCreateCmd(),
		newPackageEditCmd(),
		newPackageDeleteCmd(),
	)
	return cmd
}

func newPackageListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all hosting packages",
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			resp, err := client.ListPackages(ctx, 1, 1000)
			if err != nil {
				return fmt.Errorf("list packages: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]interface{}{
					"packages": resp.Data,
					"total":    resp.Total,
				})
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tDISK MB\tBW MB\tDOMAINS\tEMAIL\tDB\tSSH")
			for _, p := range resp.Data {
				ssh := "no"
				if p.SSHEnabled {
					ssh = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\n",
					p.ID, p.Name, p.DiskQuotaMB, p.BandwidthQuotaMB,
					p.MaxDomains, p.MaxEmailAccounts, p.MaxDatabases, ssh)
			}
			return w.Flush()
		},
	}
}

func newPackageCreateCmd() *cobra.Command {
	var (
		name        string
		diskMB      uint32
		bwMB        uint32
		domains     uint32
		emails      uint32
		databases   uint32
		ftp         uint32
		sshEnabled  bool
		cgiEnabled  bool
	)

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a hosting package",
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.CreatePackageRequest{
				Name:             name,
				DiskQuotaMB:      diskMB,
				BandwidthQuotaMB: bwMB,
				MaxDomains:       domains,
				MaxEmailAccounts: emails,
				MaxDatabases:     databases,
				MaxFTPAccounts:   ftp,
				SSHEnabled:       sshEnabled,
				CGIEnabled:       cgiEnabled,
			}

			pkg, err := client.CreatePackage(ctx, req)
			if err != nil {
				return fmt.Errorf("create package: %w", err)
			}

			if jsonOutput {
				return printJSON(pkg)
			}
			fmt.Printf("Created package %s (%s)\n", pkg.ID, pkg.Name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "package name (required)")
	cmd.Flags().Uint32Var(&diskMB, "disk-mb", 0, "disk quota in MB (0=unlimited)")
	cmd.Flags().Uint32Var(&bwMB, "bw-mb", 0, "bandwidth quota in MB (0=unlimited)")
	cmd.Flags().Uint32Var(&domains, "domains", 0, "max domains (0=unlimited)")
	cmd.Flags().Uint32Var(&emails, "emails", 0, "max email accounts (0=unlimited)")
	cmd.Flags().Uint32Var(&databases, "databases", 0, "max databases (0=unlimited)")
	cmd.Flags().Uint32Var(&ftp, "ftp", 0, "max FTP accounts (0=unlimited)")
	cmd.Flags().BoolVar(&sshEnabled, "ssh", false, "enable SSH access")
	cmd.Flags().BoolVar(&cgiEnabled, "cgi", false, "enable CGI")

	return cmd
}

func newPackageEditCmd() *cobra.Command {
	var (
		name       string
		diskMB     uint32
		bwMB       uint32
		domains    uint32
		emails     uint32
		databases  uint32
		ftp        uint32
		sshEnabled string
		cgiEnabled string
	)

	cmd := &cobra.Command{
		Use:     "edit <package-id>",
		Short:   "Edit a hosting package",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			packageID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			req := &clientapi.UpdatePackageRequest{}
			changed := false

			if cmd.Flags().Changed("name") {
				req.Name = &name
				changed = true
			}
			if cmd.Flags().Changed("disk-mb") {
				req.DiskQuotaMB = &diskMB
				changed = true
			}
			if cmd.Flags().Changed("bw-mb") {
				req.BandwidthQuotaMB = &bwMB
				changed = true
			}
			if cmd.Flags().Changed("domains") {
				req.MaxDomains = &domains
				changed = true
			}
			if cmd.Flags().Changed("emails") {
				req.MaxEmailAccounts = &emails
				changed = true
			}
			if cmd.Flags().Changed("databases") {
				req.MaxDatabases = &databases
				changed = true
			}
			if cmd.Flags().Changed("ftp") {
				req.MaxFTPAccounts = &ftp
				changed = true
			}
			if sshEnabled == "true" {
				req.SSHEnabled = boolPtr(true)
				changed = true
			} else if sshEnabled == "false" {
				req.SSHEnabled = boolPtr(false)
				changed = true
			}
			if cgiEnabled == "true" {
				req.CGIEnabled = boolPtr(true)
				changed = true
			} else if cgiEnabled == "false" {
				req.CGIEnabled = boolPtr(false)
				changed = true
			}

			if !changed {
				return fmt.Errorf("no changes specified")
			}

			pkg, err := client.UpdatePackage(ctx, packageID, req)
			if err != nil {
				return fmt.Errorf("update package: %w", err)
			}

			if jsonOutput {
				return printJSON(pkg)
			}
			fmt.Printf("Updated package %s (%s)\n", pkg.ID, pkg.Name)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "package name")
	cmd.Flags().Uint32Var(&diskMB, "disk-mb", 0, "disk quota MB")
	cmd.Flags().Uint32Var(&bwMB, "bw-mb", 0, "bandwidth MB")
	cmd.Flags().Uint32Var(&domains, "domains", 0, "max domains")
	cmd.Flags().Uint32Var(&emails, "emails", 0, "max emails")
	cmd.Flags().Uint32Var(&databases, "databases", 0, "max databases")
	cmd.Flags().Uint32Var(&ftp, "ftp", 0, "max FTP")
	cmd.Flags().StringVar(&sshEnabled, "ssh", "", "SSH access (true/false)")
	cmd.Flags().StringVar(&cgiEnabled, "cgi", "", "CGI access (true/false)")

	return cmd
}

func newPackageDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <package-id>",
		Short:   "Delete a hosting package",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			packageID := args[0]

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			client, err := newAPIClient(ctx, sharedCfg, sharedLog)
			if err != nil {
				return fmt.Errorf("create api client: %w", err)
			}

			// Fetch package to get name for confirmation
			pkg, err := client.GetPackage(ctx, packageID)
			if err != nil {
				return fmt.Errorf("fetch package: %w", err)
			}

			if !force {
				fmt.Printf("Delete package %s (%s)? [y/N]: ", pkg.ID, pkg.Name)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			if err := client.DeletePackage(ctx, packageID); err != nil {
				return fmt.Errorf("delete package: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]string{"deleted": packageID})
			}
			fmt.Printf("Deleted package %s (%s)\n", pkg.ID, pkg.Name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return cmd
}
