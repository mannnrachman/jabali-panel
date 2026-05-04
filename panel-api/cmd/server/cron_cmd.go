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

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func cronJobRepoFromDB() repository.CronJobRepository {
	return repository.NewCronJobRepository(sharedDB)
}

func newCronCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cron",
		Short: "Manage user cron jobs (systemd-user timers)",
	}
	cmd.AddCommand(
		newCronListCmd(),
		newCronAddCmd(),
		newCronUpdateCmd(),
		newCronDeleteCmd(),
		newCronRunNowCmd(),
	)
	return cmd
}

func newCronListCmd() *cobra.Command {
	var userLookup string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List cron jobs (filtered by user, or all)",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			repo := cronJobRepoFromDB()
			var rows []*models.CronJob
			var err error
			if userLookup == "" {
				rows, err = repo.ListAll(ctx)
			} else {
				u, uerr := resolveUser(ctx, userLookup)
				if uerr != nil {
					return uerr
				}
				rows, err = repo.ListByUserID(ctx, u.ID)
			}
			if err != nil {
				return fmt.Errorf("list cron jobs: %w", err)
			}
			if jsonOutput {
				return printJSON(map[string]any{"jobs": rows, "total": len(rows)})
			}
			if len(rows) == 0 {
				fmt.Println("No cron jobs.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tUSER_ID\tNAME\tSCHEDULE\tENABLED\tLAST_RUN\tLAST_EXIT")
			for _, j := range rows {
				last := "-"
				if j.LastRunAt != nil {
					last = j.LastRunAt.Format(time.RFC3339)
				}
				ec := "-"
				if j.LastExitCode != nil {
					ec = fmt.Sprintf("%d", *j.LastExitCode)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					j.ID, j.UserID, j.Name, j.Schedule, boolYN(j.Enabled), last, ec)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&userLookup, "user", "", "filter by user (id|email|username); empty = all")
	return cmd
}

func newCronAddCmd() *cobra.Command {
	var (
		userLookup string
		name       string
		schedule   string
		command    string
		disabled   bool
	)
	cmd := &cobra.Command{
		Use:     "add",
		Short:   "Add a cron job (5-field cron, allowlisted commands only)",
		Long:    "Schedule format is standard 5-field cron (e.g. '*/15 * * * *'). Command must pass cron-validate (php/wp/curl/git etc. allowlist; absolute paths only).",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			u, err := resolveUser(ctx, userLookup)
			if err != nil {
				return err
			}
			job := &models.CronJob{
				ID:       ids.NewULID(),
				UserID:   u.ID,
				Name:     name,
				Command:  command,
				Schedule: schedule,
				Enabled:  !disabled,
			}
			if err := cronJobRepoFromDB().Create(ctx, job); err != nil {
				return fmt.Errorf("create cron job: %w", err)
			}
			if jsonOutput {
				return printJSON(job)
			}
			fmt.Printf("Created cron job %s (%s) for %s. Reconciler tick (≤60s) writes the systemd timer.\n",
				job.ID, job.Name, derefStr(u.Username))
			return nil
		},
	}
	cmd.Flags().StringVar(&userLookup, "user", "", "user (id|email|username) (required)")
	cmd.Flags().StringVar(&name, "name", "", "job name (required)")
	cmd.Flags().StringVar(&schedule, "schedule", "", "5-field cron expression e.g. '*/15 * * * *' (required)")
	cmd.Flags().StringVar(&command, "command", "", "command to run (required, allowlisted)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create disabled")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("schedule")
	_ = cmd.MarkFlagRequired("command")
	return cmd
}

func newCronUpdateCmd() *cobra.Command {
	var (
		name     string
		schedule string
		command  string
		enabled  string
	)
	cmd := &cobra.Command{
		Use:     "update <job-id>",
		Short:   "Update a cron job",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			repo := cronJobRepoFromDB()
			job, err := repo.FindByID(ctx, args[0])
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("cron job %q not found", args[0])
				}
				return fmt.Errorf("find: %w", err)
			}
			changed := false
			if cmd.Flags().Changed("name") {
				job.Name = name
				changed = true
			}
			if cmd.Flags().Changed("schedule") {
				job.Schedule = schedule
				changed = true
			}
			if cmd.Flags().Changed("command") {
				job.Command = command
				changed = true
			}
			if enabled == "true" {
				job.Enabled = true
				changed = true
			} else if enabled == "false" {
				job.Enabled = false
				changed = true
			}
			if !changed {
				return fmt.Errorf("no changes specified")
			}
			if err := repo.Update(ctx, job); err != nil {
				return fmt.Errorf("update cron job: %w", err)
			}
			if jsonOutput {
				return printJSON(job)
			}
			fmt.Printf("Updated cron job %s. Reconciler will reapply on next tick.\n", job.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "")
	cmd.Flags().StringVar(&schedule, "schedule", "", "")
	cmd.Flags().StringVar(&command, "command", "", "")
	cmd.Flags().StringVar(&enabled, "enabled", "", "true|false")
	return cmd
}

func newCronDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <job-id>",
		Short:   "Delete a cron job (reconciler removes the timer on next tick)",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			repo := cronJobRepoFromDB()
			job, err := repo.FindByID(ctx, args[0])
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("cron job %q not found", args[0])
				}
				return fmt.Errorf("find: %w", err)
			}
			if !force {
				fmt.Printf("Delete cron job %s (%s, %s)? [y/N]: ", job.ID, job.Name, job.Schedule)
				var c string
				fmt.Scanln(&c)
				if c != "y" && c != "Y" {
					fmt.Println("Cancelled.")
					return nil
				}
			}
			if err := repo.Delete(ctx, job.ID); err != nil {
				return fmt.Errorf("delete cron job: %w", err)
			}
			if jsonOutput {
				return printJSON(map[string]string{"deleted": job.ID})
			}
			fmt.Printf("Deleted cron job %s.\n", job.ID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return cmd
}

func newCronRunNowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "run-now <job-id>",
		Short:   "Run a cron job immediately via the agent (synchronous)",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			repo := cronJobRepoFromDB()
			job, err := repo.FindByID(ctx, args[0])
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("cron job %q not found", args[0])
				}
				return fmt.Errorf("find: %w", err)
			}
			u, err := userRepo().FindByID(ctx, job.UserID)
			if err != nil {
				return fmt.Errorf("lookup user: %w", err)
			}
			raw, err := sharedAgent.Call(ctx, "cron.run_now", map[string]any{
				"user_id":  u.ID,
				"username": derefStr(u.Username),
				"job_id":   job.ID,
			})
			if err != nil {
				return fmt.Errorf("cron.run_now: %w", err)
			}
			var resp struct {
				ExitCode int    `json:"exit_code"`
				Stdout   string `json:"stdout,omitempty"`
				Stderr   string `json:"stderr,omitempty"`
			}
			_ = json.Unmarshal(raw, &resp)
			if jsonOutput {
				return printJSON(resp)
			}
			fmt.Printf("Exit: %d\n", resp.ExitCode)
			if resp.Stdout != "" {
				fmt.Println("--- stdout ---")
				fmt.Println(resp.Stdout)
			}
			if resp.Stderr != "" {
				fmt.Println("--- stderr ---")
				fmt.Println(resp.Stderr)
			}
			return nil
		},
	}
}
