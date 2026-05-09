// `jabali backup scheduler tick` cobra subcommand.
//
// Triggers one synchronous enqueue + dispatch pass of the backup
// scheduler (same code path serve.go's long-running goroutine
// invokes every 60s + 10s). Useful for bootstrap-script tests that
// want a deterministic 'create-schedule + tick + assert-job-row'
// sequence without waiting on real-time.
//
// Idempotent: ticking with no due schedules is a no-op. Running
// against an already-active panel-api process double-fires the
// dispatch slot but the in-memory inFlight map dedupes — worst
// case a few extra log lines.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/backupscheduler"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newBackupSchedulerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scheduler",
		Short: "Backup scheduler ops (manual tick / debug)",
	}
	cmd.AddCommand(newBackupSchedulerTickCmd())
	return cmd
}

func newBackupSchedulerTickCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tick",
		Short: "Run one enqueue + dispatch pass of the backup scheduler synchronously",
		Long: `Triggers Scheduler.TickOnce — same code path the serve.go
long-running goroutine fires every 60s (enqueue) + 10s (dispatch).
Useful for bootstrap scripts asserting on schedule firing without
real-time waits.

Builds its own Scheduler with sharedDB + sharedAgent; doesn't talk
to the running panel-api. Both routes hit the same DB rows so an
operator running this against a live panel sees the in-memory
inFlight map dedupe duplicate dispatches.`,
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			s := backupscheduler.New(backupscheduler.Deps{
				Schedules:      repository.NewBackupScheduleRepository(sharedDB),
				Jobs:           repository.NewBackupJobRepository(sharedDB),
				Destinations:   repository.NewBackupDestinationRepository(sharedDB),
				Users:          repository.NewUserRepository(sharedDB),
				Databases:      repository.NewDatabaseRepository(sharedDB),
				DatabaseUsers:  repository.NewDatabaseUserRepository(sharedDB),
				DatabaseGrants: repository.NewDatabaseUserGrantRepository(sharedDB),
				Domains:        repository.NewDomainRepository(sharedDB),
				Mailboxes:      repository.NewMailboxRepository(sharedDB),
				AppInstalls:    repository.NewWordPressInstallRepository(sharedDB),
				Settings:       repository.NewServerSettingsRepository(sharedDB),
				SSLCerts:       repository.NewSSLCertificateRepository(sharedDB),
				PHPPools:       repository.NewPHPPoolRepository(sharedDB),
				PHPPoolIni:     repository.NewPHPPoolIniOverrideRepository(sharedDB),
				Forwarders:     repository.NewEmailForwarderRepository(sharedDB),
				Autoresponders: repository.NewEmailAutoresponderRepository(sharedDB),
				MailboxShares:  repository.NewMailboxShareRepository(sharedDB),
				DNSSECKeys:     repository.NewDNSSECKeyRepository(sharedDB),
				SSHKeys:        repository.NewSSHKeyRepository(sharedDB),
				CronJobs:       repository.NewCronJobRepository(sharedDB),
				LimitOverrides: repository.NewUserLimitOverrideRepository(sharedDB),
				EgressPolicies: repository.NewUserEgressPolicyRepository(sharedDB),
				EgressRequests: repository.NewUserEgressRequestRepository(sharedDB),
				Agent:          sharedAgent,
				SSOKey:         nil, // tick path only enqueues; sso key needed only for cred decrypt during dispatch
				Log:            sharedLog,
			})
			if s == nil {
				return fmt.Errorf("scheduler.New returned nil — required deps missing (check serve.go's Deps assembly)")
			}
			s.TickOnce(ctx)
			fmt.Fprintln(cmd.OutOrStdout(), "scheduler tick: enqueue + dispatch passes complete")
			return nil
		},
	}
}
