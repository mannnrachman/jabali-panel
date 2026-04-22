package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// newPdnsCmd mounts `jabali pdns …`. Only child currently is `backfill`.
func newPdnsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pdns",
		Short: "PowerDNS helpers (recursor forwarders, etc.)",
	}
	cmd.AddCommand(newPdnsBackfillCmd())
	return cmd
}

type pdnsBackfillOpts struct {
	yes            bool
	verbose        bool
	noninteractive bool
}

func newPdnsBackfillCmd() *cobra.Command {
	var opts pdnsBackfillOpts
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Converge /etc/powerdns/recursor.forwards with the panel database",
		Long: `Walks enabled domains in the panel DB plus the panel's self-zone, and
compares against the recursor's current forward-zones-file state.

Default is --dry-run: prints the planned adds/removes/no-ops, exits 0,
makes no changes. Use --yes to actually apply via the panel-agent's
pdns.recursor_add_zone / pdns.recursor_remove_zone commands (idempotent
atomic writes with validator + rec_control reload + SOA post-probe +
rollback).

This CLI is the operator-driven converge path. The reconciler runs
the same operations every tick (default 60s), so in steady state
backfill's dry-run should report all 'noop'. When it doesn't, the
reconciler is either paused, broken, or behind — investigate before
applying --yes.`,
		PreRunE: requireDBAndAgent,
		RunE: func(_ *cobra.Command, _ []string) error {
			return opts.run(context.Background())
		},
	}
	cmd.Flags().BoolVar(&opts.yes, "yes", false, "apply the plan (default is dry-run)")
	cmd.Flags().BoolVar(&opts.verbose, "verbose", false, "print per-zone detail")
	cmd.Flags().BoolVar(&opts.noninteractive, "no-confirm", false,
		"skip the y/N confirmation when --yes is used (for scripted runs; "+
			"otherwise set JABALI_PDNS_BACKFILL_NONINTERACTIVE=1)")
	return cmd
}

// recursorAction describes one line of the plan table.
type recursorAction struct {
	Zone      string
	Forwarder string
	Action    string // "add" | "update" | "remove" | "noop"
}

// actualForwarder is the pure-function input shape for computeBackfillPlan
// — decoupled from the agent JSON schema so the unit test doesn't need
// to reach into the agent package.
type actualForwarder struct {
	Addr string
	Port int
}

const backfillForwarderAddr = "127.0.0.1"
const backfillForwarderPort = 5300

// computeBackfillPlan diffs the desired zone set (DB + self-zone) against
// the actual recursor.forwards state and returns a sorted plan. Pure
// function — tested in pdns_cmd_test.go.
func computeBackfillPlan(desired map[string]bool, actual map[string]actualForwarder) []recursorAction {
	var plan []recursorAction
	for zone := range desired {
		existing, ok := actual[zone]
		switch {
		case !ok:
			plan = append(plan, recursorAction{
				Zone:      zone,
				Forwarder: fmt.Sprintf("%s:%d", backfillForwarderAddr, backfillForwarderPort),
				Action:    "add",
			})
		case existing.Addr == backfillForwarderAddr && existing.Port == backfillForwarderPort:
			plan = append(plan, recursorAction{
				Zone:      zone,
				Forwarder: fmt.Sprintf("%s:%d", existing.Addr, existing.Port),
				Action:    "noop",
			})
		default:
			plan = append(plan, recursorAction{
				Zone:      zone,
				Forwarder: fmt.Sprintf("%s:%d", backfillForwarderAddr, backfillForwarderPort),
				Action:    "update",
			})
		}
	}
	for zone := range actual {
		if desired[zone] {
			continue
		}
		plan = append(plan, recursorAction{
			Zone:      zone,
			Forwarder: "—",
			Action:    "remove",
		})
	}
	sort.SliceStable(plan, func(i, j int) bool {
		if plan[i].Action != plan[j].Action {
			rank := map[string]int{"add": 0, "update": 1, "noop": 2, "remove": 3}
			return rank[plan[i].Action] < rank[plan[j].Action]
		}
		return plan[i].Zone < plan[j].Zone
	})
	return plan
}

func (o *pdnsBackfillOpts) run(ctx context.Context) error {
	// 1. Walk enabled domains from the DB.
	domRepo := repository.NewDomainRepository(sharedDB)
	desired := map[string]bool{}

	// Enumerate ALL domains (no pagination filter); a panel with thousands
	// of domains isn't the M6.3 scope, but add a visible cap so runaway
	// queries don't OOM.
	opts := repository.ListOptions{Limit: 10000}
	allDomains, _, err := domRepo.List(ctx, opts)
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	for _, d := range allDomains {
		if d.IsEnabled {
			desired[d.Name] = true
		}
	}

	// 2. Add the self-zone.
	ssRepo := repository.NewServerSettingsRepository(sharedDB)
	if ss, sErr := ssRepo.Get(ctx); sErr == nil && ss != nil && ss.Hostname != "" {
		desired[ss.Hostname] = true
	} else if o.verbose {
		fmt.Fprintln(os.Stderr, "note: server settings hostname empty — skipping self-zone backfill")
	}

	// 3. Fetch current recursor.forwards via the agent.
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := sharedAgent.Call(listCtx, "pdns.recursor_list", nil)
	if err != nil {
		return fmt.Errorf("agent pdns.recursor_list: %w", err)
	}
	var listResp struct {
		Entries []struct {
			Zone string `json:"zone"`
			Addr string `json:"addr"`
			Port int    `json:"port"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &listResp); err != nil {
		return fmt.Errorf("decode recursor_list: %w", err)
	}
	actual := map[string]struct {
		Addr string
		Port int
	}{}
	for _, e := range listResp.Entries {
		actual[e.Zone] = struct {
			Addr string
			Port int
		}{e.Addr, e.Port}
	}

	// 4. Compute plan.
	actualMap := make(map[string]actualForwarder, len(actual))
	for z, e := range actual {
		actualMap[z] = actualForwarder{Addr: e.Addr, Port: e.Port}
	}
	plan := computeBackfillPlan(desired, actualMap)

	// 5. Print.
	o.printPlan(plan)

	// 6. Dry-run path.
	if !o.yes {
		if countNonNoop(plan) > 0 {
			fmt.Fprintln(os.Stderr, "\n(dry-run; pass --yes to apply)")
		}
		return nil
	}

	// 7. Confirmation (--yes).
	if !o.noninteractive && os.Getenv("JABALI_PDNS_BACKFILL_NONINTERACTIVE") != "1" {
		n := countNonNoop(plan)
		if n == 0 {
			// Nothing to do; skip the prompt.
			return nil
		}
		fmt.Fprintf(os.Stderr, "\nApply %d change(s)? [y/N]: ", n)
		var resp string
		fmt.Scanln(&resp)
		if strings.ToLower(strings.TrimSpace(resp)) != "y" && strings.ToLower(strings.TrimSpace(resp)) != "yes" {
			fmt.Fprintln(os.Stderr, "aborted.")
			return fmt.Errorf("user did not confirm")
		}
	}

	// 8. Apply.
	return o.apply(ctx, plan)
}

func (o *pdnsBackfillOpts) printPlan(plan []recursorAction) {
	// Compute column widths.
	zoneW := len("ZONE")
	fwdW := len("FORWARDER")
	for _, p := range plan {
		if len(p.Zone) > zoneW {
			zoneW = len(p.Zone)
		}
		if len(p.Forwarder) > fwdW {
			fwdW = len(p.Forwarder)
		}
	}
	fmt.Printf("%-*s  %-*s  %s\n", zoneW, "ZONE", fwdW, "FORWARDER", "ACTION")
	fmt.Printf("%s  %s  %s\n", strings.Repeat("-", zoneW), strings.Repeat("-", fwdW), strings.Repeat("-", 6))
	for _, p := range plan {
		fmt.Printf("%-*s  %-*s  %s\n", zoneW, p.Zone, fwdW, p.Forwarder, p.Action)
	}
	if len(plan) == 0 {
		fmt.Println("(no zones — DB empty and recursor.forwards empty)")
	}
}

func (o *pdnsBackfillOpts) apply(ctx context.Context, plan []recursorAction) error {
	var errs []string
	for _, p := range plan {
		switch p.Action {
		case "add", "update":
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := sharedAgent.Call(cctx, "pdns.recursor_add_zone", map[string]any{
				"zone": p.Zone,
				"addr": backfillForwarderAddr,
				"port": backfillForwarderPort,
			})
			cancel()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s add: %v", p.Zone, err))
				fmt.Fprintf(os.Stderr, "ERR  %s  add failed: %v\n", p.Zone, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "OK   %s  added\n", p.Zone)
		case "remove":
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := sharedAgent.Call(cctx, "pdns.recursor_remove_zone", map[string]any{
				"zone": p.Zone,
			})
			cancel()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s remove: %v", p.Zone, err))
				fmt.Fprintf(os.Stderr, "ERR  %s  remove failed: %v\n", p.Zone, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "OK   %s  removed\n", p.Zone)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d error(s) during apply:\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	return nil
}

func countNonNoop(plan []recursorAction) int {
	n := 0
	for _, p := range plan {
		if p.Action != "noop" {
			n++
		}
	}
	return n
}

// Compile-time keep — ensures models package stays imported for the test
// file's type shape even if the production code eliminates the reference.
var _ = models.Domain{}
