package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
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
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			pkgs, _, err := packageRepoFromDB().List(ctx, 0, 1000)
			if err != nil {
				return fmt.Errorf("list packages: %w", err)
			}

			if jsonOutput {
				return printJSON(pkgs)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tDISK MB\tBW MB\tDOMAINS\tEMAIL\tDB\tSSH")
			for _, p := range pkgs {
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
		name       string
		diskMB     uint32
		bwMB       uint32
		domains    uint32
		emails     uint32
		databases  uint32
		ftp        uint32
		sshEnabled bool
	)

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a hosting package",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			now := time.Now().UTC()
			pkg := &models.HostingPackage{
				ID:               ids.NewULID(),
				Name:             name,
				DiskQuotaMB:      diskMB,
				BandwidthQuotaMB: bwMB,
				MaxDomains:       domains,
				MaxEmailAccounts: emails,
				MaxDatabases:     databases,
				MaxFTPAccounts:   ftp,
				SSHEnabled:       sshEnabled,
				CreatedAt:        now,
				UpdatedAt:        now,
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			if err := packageRepoFromDB().Create(ctx, pkg); err != nil {
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
	)

	cmd := &cobra.Command{
		Use:     "edit <package-id>",
		Short:   "Edit a hosting package",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			repo := packageRepoFromDB()
			pkg, err := repo.FindByID(ctx, args[0])
			if err != nil {
				return fmt.Errorf("find package: %w", err)
			}

			changed := false
			if cmd.Flags().Changed("name") {
				pkg.Name = name
				changed = true
			}
			if cmd.Flags().Changed("disk-mb") {
				pkg.DiskQuotaMB = diskMB
				changed = true
			}
			if cmd.Flags().Changed("bw-mb") {
				pkg.BandwidthQuotaMB = bwMB
				changed = true
			}
			if cmd.Flags().Changed("domains") {
				pkg.MaxDomains = domains
				changed = true
			}
			if cmd.Flags().Changed("emails") {
				pkg.MaxEmailAccounts = emails
				changed = true
			}
			if cmd.Flags().Changed("databases") {
				pkg.MaxDatabases = databases
				changed = true
			}
			if cmd.Flags().Changed("ftp") {
				pkg.MaxFTPAccounts = ftp
				changed = true
			}
			if sshEnabled == "true" {
				pkg.SSHEnabled = true
				changed = true
			} else if sshEnabled == "false" {
				pkg.SSHEnabled = false
				changed = true
			}

			if !changed {
				return fmt.Errorf("no changes specified")
			}

			pkg.UpdatedAt = time.Now().UTC()
			if err := repo.Update(ctx, pkg); err != nil {
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

	return cmd
}

func newPackageDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <package-id>",
		Short:   "Delete a hosting package",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			repo := packageRepoFromDB()
			pkg, err := repo.FindByID(ctx, args[0])
			if err != nil {
				return fmt.Errorf("find package: %w", err)
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

			if err := repo.Delete(ctx, pkg.ID); err != nil {
				return fmt.Errorf("delete package: %w", err)
			}

			if jsonOutput {
				return printJSON(map[string]string{"deleted": pkg.ID})
			}
			fmt.Printf("Deleted package %s (%s)\n", pkg.ID, pkg.Name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return cmd
}
