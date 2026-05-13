// mailbox_extras_cmd.go — M6.5 CLI surfaces for autoresponder + forwarder.
// Mirrors the HTTP handlers in panel-api/internal/api/mailbox_*.go but
// goes direct DB + agent so operators can drive the panel from a script
// without a Kratos session. Same lifecycle: panel writes the row, the
// reconciler converges Stalwart on the next tick; this file additionally
// fires a best-effort inline agent call so the change is visible without
// waiting for the next reconcile.

package main

import (
	"context"
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

// ---- repo helpers ---------------------------------------------------------

func forwarderRepoFromDB() repository.EmailForwarderRepository {
	return repository.NewEmailForwarderRepository(sharedDB)
}

func autoresponderRepoFromDB() repository.EmailAutoresponderRepository {
	return repository.NewEmailAutoresponderRepository(sharedDB)
}

// findMailboxByEmailCLI is the lookup that every M6.5 subcommand needs.
// Returns the mailbox row + the parent domain (autoresponder/forwarder
// agent payloads need the domain name for routing).
func findMailboxByEmailCLI(ctx context.Context, email string) (*models.Mailbox, *models.Domain, error) {
	mb, err := mailboxRepoFromDB().FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, fmt.Errorf("mailbox %s not found", email)
		}
		return nil, nil, fmt.Errorf("lookup mailbox: %w", err)
	}
	dom, err := domainRepoFromDB().FindByID(ctx, mb.DomainID)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup domain: %w", err)
	}
	return mb, dom, nil
}

// ---- autoresponder --------------------------------------------------------

func newMailboxAutoresponderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autoresponder",
		Short: "Manage per-mailbox vacation responders",
	}
	cmd.AddCommand(
		newMailboxAutoresponderSetCmd(),
		newMailboxAutoresponderClearCmd(),
		newMailboxAutoresponderShowCmd(),
	)
	return cmd
}

func newMailboxAutoresponderSetCmd() *cobra.Command {
	var (
		subject  string
		body     string
		htmlBody string
		from     string
		to       string
	)
	cmd := &cobra.Command{
		Use:     "set <email>",
		Short:   "Enable an autoresponder for a mailbox",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			email := args[0]
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			mb, dom, err := findMailboxByEmailCLI(ctx, email)
			if err != nil {
				return err
			}

			if subject == "" {
				return fmt.Errorf("--subject is required")
			}
			if body == "" && htmlBody == "" {
				return fmt.Errorf("at least one of --body or --html-body is required")
			}

			ar := &models.EmailAutoresponder{
				MailboxID: mb.ID,
				Enabled:   true,
				Subject:   &subject,
				ManagedBy: "m6.5",
			}
			if body != "" {
				v := body
				ar.TextBody = &v
			}
			if htmlBody != "" {
				v := htmlBody
				ar.HTMLBody = &v
			}
			if from != "" {
				t, err := time.Parse(time.RFC3339, from)
				if err != nil {
					return fmt.Errorf("--from must be RFC3339 (e.g. 2026-05-01T00:00:00Z): %w", err)
				}
				ar.FromDate = &t
			}
			if to != "" {
				t, err := time.Parse(time.RFC3339, to)
				if err != nil {
					return fmt.Errorf("--to must be RFC3339: %w", err)
				}
				ar.ToDate = &t
			}

			if err := autoresponderRepoFromDB().Update(ctx, ar); err != nil {
				return fmt.Errorf("save autoresponder: %w", err)
			}

			// Best-effort inline push to Stalwart. Reconciler re-asserts
			// on drift, so a swallowed error doesn't lose the change.
			params := map[string]any{
				"mailbox_email": mb.LocalPart + "@" + dom.Name,
				"enabled":       true,
				"subject":       subject,
			}
			if body != "" {
				params["text_body"] = body
			}
			if htmlBody != "" {
				params["html_body"] = htmlBody
			}
			if ar.FromDate != nil {
				params["from_date"] = ar.FromDate.UTC().Format(time.RFC3339)
			}
			if ar.ToDate != nil {
				params["to_date"] = ar.ToDate.UTC().Format(time.RFC3339)
			}
			notifyAgentMailbox(ctx, "autoresponder.set", params)

			if jsonOutput {
				return printJSON(map[string]any{
					"mailbox": email,
					"enabled": true,
					"subject": subject,
				})
			}
			fmt.Printf("Autoresponder enabled for %s\n", email)
			return nil
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "Subject line (required)")
	cmd.Flags().StringVar(&body, "body", "", "Plain text body (optional if --html-body set)")
	cmd.Flags().StringVar(&htmlBody, "html-body", "", "HTML body (optional)")
	cmd.Flags().StringVar(&from, "from", "", "Start date (RFC3339, e.g. 2026-05-01T00:00:00Z)")
	cmd.Flags().StringVar(&to, "to", "", "End date (RFC3339)")
	return cmd
}

func newMailboxAutoresponderClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "clear <email>",
		Short:   "Disable + delete the autoresponder for a mailbox",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			email := args[0]
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			mb, dom, err := findMailboxByEmailCLI(ctx, email)
			if err != nil {
				return err
			}
			if err := autoresponderRepoFromDB().Delete(ctx, mb.ID); err != nil &&
				!errors.Is(err, repository.ErrNotFound) {
				return fmt.Errorf("delete autoresponder: %w", err)
			}
			notifyAgentMailbox(ctx, "autoresponder.set", map[string]any{
				"mailbox_email": mb.LocalPart + "@" + dom.Name,
				"enabled":       false,
			})
			fmt.Printf("Autoresponder cleared for %s\n", email)
			return nil
		},
	}
}

func newMailboxAutoresponderShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <email>",
		Short:   "Print the current autoresponder for a mailbox",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			email := args[0]
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			mb, _, err := findMailboxByEmailCLI(ctx, email)
			if err != nil {
				return err
			}
			ar, err := autoresponderRepoFromDB().FindByMailboxID(ctx, mb.ID)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					if jsonOutput {
						return printJSON(map[string]any{"mailbox": email, "enabled": false})
					}
					fmt.Printf("No autoresponder set for %s\n", email)
					return nil
				}
				return fmt.Errorf("lookup autoresponder: %w", err)
			}
			if jsonOutput {
				return printJSON(ar)
			}
			fmt.Printf("Mailbox:   %s\n", email)
			fmt.Printf("Enabled:   %v\n", ar.Enabled)
			if ar.Subject != nil {
				fmt.Printf("Subject:   %s\n", *ar.Subject)
			}
			if ar.FromDate != nil {
				fmt.Printf("From:      %s\n", ar.FromDate.UTC().Format(time.RFC3339))
			}
			if ar.ToDate != nil {
				fmt.Printf("To:        %s\n", ar.ToDate.UTC().Format(time.RFC3339))
			}
			if ar.TextBody != nil {
				fmt.Printf("Text body: %s\n", *ar.TextBody)
			}
			if ar.HTMLBody != nil {
				fmt.Printf("HTML body: %d bytes\n", len(*ar.HTMLBody))
			}
			return nil
		},
	}
}

// ---- forwarder ------------------------------------------------------------

func newMailboxForwarderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forwarder",
		Short: "Manage per-mailbox aliases + external forwards",
	}
	cmd.AddCommand(
		newMailboxForwarderAddCmd(),
		newMailboxForwarderListCmd(),
		newMailboxForwarderRemoveCmd(),
	)
	return cmd
}

func newMailboxForwarderAddCmd() *cobra.Command {
	var (
		fwdType   string
		localPart string
		target    string
	)
	cmd := &cobra.Command{
		Use:     "add <email>",
		Short:   "Add an alias or external forwarder to a mailbox",
		Long:    `Type 'alias' delivers <local>@<domain> mail to <email>. Type 'external' forwards <email> to <target>.`,
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			email := args[0]
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			mb, dom, err := findMailboxByEmailCLI(ctx, email)
			if err != nil {
				return err
			}
			if fwdType != "alias" && fwdType != "external" {
				return fmt.Errorf("--type must be 'alias' or 'external'")
			}
			if fwdType == "alias" && localPart == "" {
				return fmt.Errorf("--local is required for alias")
			}
			if fwdType == "external" && target == "" {
				return fmt.Errorf("--target is required for external")
			}

			f := &models.EmailForwarder{
				ID:        ids.NewULID(),
				MailboxID: &mb.ID,
				DomainID:  dom.ID,
				Type:      fwdType,
				Enabled:   true,
				ManagedBy: "m6.5",
			}
			if fwdType == "alias" {
				lp := localPart
				f.LocalPart = &lp
				// Alias target = the mailbox itself; matches the HTTP
				// handler's default when target is omitted.
				f.Target = mb.LocalPart + "@" + dom.Name
			} else {
				f.Target = target
			}
			if err := forwarderRepoFromDB().Create(ctx, f); err != nil {
				return fmt.Errorf("create forwarder: %w", err)
			}
			notifyAgentMailbox(ctx, "domain.email_apply", map[string]any{
				"domain_id":   dom.ID,
				"domain_name": dom.Name,
			})
			if jsonOutput {
				return printJSON(f)
			}
			fmt.Printf("Forwarder %s added (id=%s)\n", fwdType, f.ID)
			if fwdType == "alias" {
				fmt.Printf("  %s@%s -> %s\n", localPart, dom.Name, f.Target)
			} else {
				fmt.Printf("  %s -> %s\n", email, target)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fwdType, "type", "", "alias | external (required)")
	cmd.Flags().StringVar(&localPart, "local", "", "Alias local part (required for type=alias)")
	cmd.Flags().StringVar(&target, "target", "", "External destination email (required for type=external)")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func newMailboxForwarderListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list <email>",
		Short:   "List forwarders attached to a mailbox",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			email := args[0]
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			mb, dom, err := findMailboxByEmailCLI(ctx, email)
			if err != nil {
				return err
			}
			rows, _, err := forwarderRepoFromDB().ListByMailboxID(ctx, mb.ID, repository.ListOptions{Limit: 200})
			if err != nil {
				return fmt.Errorf("list forwarders: %w", err)
			}
			if jsonOutput {
				return printJSON(map[string]any{"mailbox": email, "forwarders": rows, "total": len(rows)})
			}
			if len(rows) == 0 {
				fmt.Printf("No forwarders for %s\n", email)
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTYPE\tFROM\tTO\tENABLED")
			for _, f := range rows {
				lp := ""
				if f.LocalPart != nil {
					lp = *f.LocalPart
				}
				from := f.Target
				if f.Type == "alias" {
					from = lp + "@" + dom.Name
				} else {
					from = email
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\n", f.ID, f.Type, from, f.Target, f.Enabled)
			}
			return w.Flush()
		},
	}
}

func newMailboxForwarderRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <forwarder-id>",
		Short:   "Delete a forwarder by ID (find via 'forwarder list')",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			f, err := forwarderRepoFromDB().FindByID(ctx, id)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return fmt.Errorf("forwarder %s not found", id)
				}
				return fmt.Errorf("lookup forwarder: %w", err)
			}
			dom, err := domainRepoFromDB().FindByID(ctx, f.DomainID)
			if err != nil {
				return fmt.Errorf("lookup domain: %w", err)
			}
			if err := forwarderRepoFromDB().Delete(ctx, id); err != nil {
				return fmt.Errorf("delete forwarder: %w", err)
			}
			notifyAgentMailbox(ctx, "domain.email_apply", map[string]any{
				"domain_id":   dom.ID,
				"domain_name": dom.Name,
			})
			fmt.Printf("Forwarder %s deleted\n", id)
			return nil
		},
	}
}
