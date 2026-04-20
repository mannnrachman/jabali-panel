package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
)

// `jabali app e2e` — install every registered app on one domain as
// unique subdirectories, poll, delete, then print a pass/fail matrix.
// Goal: stop fixing per-app upstream URL pins one screenshot at a
// time. One run names every broken app.
//
// The harness runs against a real test VM through the same service
// the HTTP handler uses, so what passes here passes for the UI.

type e2eResult struct {
	AppType     string
	InstallID   string
	Subdir      string
	Status      string
	Outcome     string // "pass" | "fail"
	Duration    time.Duration
	Error       string
	DeleteError string
}

func newAppE2ECmd() *cobra.Command {
	var (
		domainID   string
		baseSubdir string
		only       []string
		skip       []string
		keep       bool
		waitSec    int
		stopOnFail bool
	)

	cmd := &cobra.Command{
		Use:   "e2e",
		Short: "Install every app on a domain, report pass/fail, then delete",
		Long: `End-to-end smoke test of the application installer framework.

For each registered app_type:
  1. install as a unique subdir under --domain-id
  2. poll until status is ready/failed
  3. delete the install (unless --keep)

Final output is a matrix. Useful for catching upstream URL drift,
missing system packages, and per-app extraction quirks in one run.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if domainID == "" {
				return fmt.Errorf("--domain-id is required (the domain everything installs under)")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Hour)
			defer cancel()

			registry := apps.New()
			if err := apps.RegisterDefaults(registry); err != nil {
				return fmt.Errorf("register app defaults: %w", err)
			}
			entries := filterEntries(registry.List(), only, skip)
			if len(entries) == 0 {
				return fmt.Errorf("no apps to test (registry empty or all filtered)")
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			fmt.Printf("Running e2e on %d app(s) against domain %s\n\n", len(entries), domainID)

			results := make([]e2eResult, 0, len(entries))
			for _, app := range entries {
				res := runOneE2E(ctx, app, domainID, baseSubdir, keep, waitSec)
				results = append(results, res)
				printResultLine(res)
				if stopOnFail && res.Outcome != "pass" {
					fmt.Println("\n--stop-on-fail set; aborting remaining apps")
					break
				}
			}

			fmt.Println()
			printResultMatrix(results)
			return summaryExitErr(results)
		},
	}

	cmd.Flags().StringVar(&domainID, "domain-id", "", "Domain ID to install all apps under (required)")
	cmd.Flags().StringVar(&baseSubdir, "base-subdir", "e2e", "Subdir prefix; each app installs under <prefix>_<app>_<rand>")
	cmd.Flags().StringSliceVar(&only, "only", nil, "Only run these app_types (comma-separated)")
	cmd.Flags().StringSliceVar(&skip, "skip", nil, "Skip these app_types (comma-separated)")
	cmd.Flags().BoolVar(&keep, "keep", false, "Don't delete installs after the run (debug)")
	cmd.Flags().IntVar(&waitSec, "wait-timeout", 600, "Per-app install timeout in seconds")
	cmd.Flags().BoolVar(&stopOnFail, "stop-on-fail", false, "Stop the sweep after the first failure")
	return cmd
}

func filterEntries(all []apps.App, only, skip []string) []apps.App {
	onlySet := toSet(only)
	skipSet := toSet(skip)
	out := make([]apps.App, 0, len(all))
	for _, e := range all {
		if len(onlySet) > 0 && !onlySet[e.Name] {
			continue
		}
		if skipSet[e.Name] {
			continue
		}
		out = append(out, e)
	}
	return out
}

func toSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" {
			out[x] = true
		}
	}
	return out
}

// runOneE2E runs a single app's install→wait→delete cycle. Always
// returns a populated row even on failure — the matrix wants every
// row, not an early exit on the first broken app.
func runOneE2E(parent context.Context, app apps.App, domainID, baseSubdir string, keep bool, waitSec int) e2eResult {
	start := time.Now()
	subdir := buildSubdir(baseSubdir, app.Name)
	res := e2eResult{AppType: app.Name, Subdir: subdir}

	params, err := synthesizeParams(app)
	if err != nil {
		res.Outcome = "fail"
		res.Error = "synthesize params: " + err.Error()
		res.Duration = time.Since(start)
		return res
	}

	createCtx, cancel := context.WithTimeout(parent, 60*time.Second)
	resp, err := installAppDirect(createCtx, api.InstallParams{
		AppType:      app.Name,
		DomainID:     domainID,
		Subdirectory: subdir,
		Params:       params,
	})
	cancel()
	if err != nil {
		res.Outcome = "fail"
		res.Error = "install: " + trimError(err.Error())
		res.Duration = time.Since(start)
		return res
	}
	res.InstallID = resp.Install.ID
	res.Status = resp.Install.Status

	final, waitErr := pollInstallStatus(parent, resp.Install.ID, time.Duration(waitSec)*time.Second)
	if final != nil {
		res.Status = final.Status
		if final.LastError != "" {
			res.Error = trimError(final.LastError)
		}
	}
	if waitErr != nil && res.Error == "" {
		res.Error = "wait: " + trimError(waitErr.Error())
	}

	if final != nil && final.Status == "ready" {
		res.Outcome = "pass"
	} else {
		res.Outcome = "fail"
	}

	if !keep {
		delCtx, delCancel := context.WithTimeout(parent, 6*time.Minute)
		if _, dErr := deleteAppDirect(delCtx, resp.Install.ID); dErr != nil {
			res.DeleteError = trimError(dErr.Error())
		}
		delCancel()
	}

	res.Duration = time.Since(start)
	return res
}

// buildSubdir guarantees uniqueness across reruns so a stale row
// (failed-but-undeleted) doesn't poison this run with a 409.
//
// Output must satisfy validateSubdirectory's `^[a-z0-9][a-z0-9_-]{0,63}$`:
// no leading slash, lowercase alnum start, no other punctuation. We
// build `<base>_<app>_<rand6>`, replacing illegal chars in base with
// underscores. The regex allows hyphens, but underscore-separators
// keep the slug single-token-looking (matches reservedSubdirectories
// style).
func buildSubdir(base, appType string) string {
	rnd := make([]byte, 3)
	_, _ = rand.Read(rnd)
	suffix := hex.EncodeToString(rnd)
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, base)
	clean = strings.Trim(clean, "_")
	if clean == "" {
		return appType + "_" + suffix
	}
	return clean + "_" + appType + "_" + suffix
}

// synthesizeParams produces a value for every required key. Optional
// keys are left out so the API exercises its default-fill path —
// regression coverage for "operator left blank".
func synthesizeParams(app apps.App) (map[string]interface{}, error) {
	out := make(map[string]interface{})
	for key, spec := range app.InstallParamSchema {
		if !spec.Required {
			continue
		}
		if spec.Default != nil {
			out[key] = spec.Default
			continue
		}
		val, err := defaultForType(spec, app.Name, key)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", key, err)
		}
		out[key] = val
	}
	return out, nil
}

// defaultForType returns a synthetic value for the given param type.
// New types added to apps.ParamSpec without a case here surface as an
// explicit error rather than silent skip — fail closed on schema drift.
func defaultForType(spec apps.ParamSpec, appName, key string) (interface{}, error) {
	switch spec.Type {
	case "email":
		return fmt.Sprintf("e2e+%s@example.com", appName), nil
	case "string":
		return "E2E " + appName, nil
	case "password":
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		return hex.EncodeToString(buf), nil
	case "url":
		return "https://example.com", nil
	case "int", "integer", "number":
		return 1, nil
	case "bool", "boolean":
		return true, nil
	case "enum":
		if len(spec.Values) == 0 {
			return nil, fmt.Errorf("enum has no values")
		}
		return spec.Values[0], nil
	}
	return nil, fmt.Errorf("unknown type %q (key=%s, app=%s)", spec.Type, key, appName)
}

func trimError(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

func printResultLine(r e2eResult) {
	icon := "PASS"
	if r.Outcome != "pass" {
		icon = "FAIL"
	}
	fmt.Printf("[%s] %-12s subdir=%-32s status=%-8s dur=%-6s\n",
		icon, r.AppType, r.Subdir, r.Status, r.Duration.Truncate(time.Second))
	if r.Error != "" {
		fmt.Printf("        error: %s\n", r.Error)
	}
	if r.DeleteError != "" {
		fmt.Printf("        delete-error: %s\n", r.DeleteError)
	}
}

func printResultMatrix(results []e2eResult) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tOUTCOME\tSTATUS\tDURATION\tERROR")
	for _, r := range results {
		errCol := r.Error
		if errCol == "" {
			errCol = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.AppType, r.Outcome, r.Status, r.Duration.Truncate(time.Second), errCol)
	}
	w.Flush()

	pass, fail := 0, 0
	for _, r := range results {
		if r.Outcome == "pass" {
			pass++
		} else {
			fail++
		}
	}
	fmt.Printf("\nTotal: %d pass, %d fail\n", pass, fail)
}

// summaryExitErr returns non-nil when ANY app failed so a CI invocation
// gets a non-zero exit. The error itself is intentionally short — the
// matrix above already names every broken app.
func summaryExitErr(results []e2eResult) error {
	for _, r := range results {
		if r.Outcome != "pass" {
			return fmt.Errorf("one or more apps failed (see matrix above)")
		}
	}
	return nil
}
