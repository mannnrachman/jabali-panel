package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// newAdminSliceCutoverCmd implements Step 6 of plans/per-user-systemd-slices.md.
// It masks the distro's global `php<v>-fpm.service` after verifying that every
// user has a working per-user FPM master. Destructive — writes to systemd and
// disables the distro unit. Always run `--dry-run` first on production.
func newAdminSliceCutoverCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "slice-cutover",
		Short: "Migrate FPM from global master to per-user masters and mask distro units",
		Long: `Slice cutover (Step 6 of the per-user-systemd-slices plan).

Phase A (preflight, always runs):
  - For every user with a Linux username, assert a jabali-user-<user>.slice
    unit exists and jabali-fpm@<user>.service is active.
  - Enumerate installed PHP versions by scanning /etc/php/*/fpm/pool.d.
  - For each user, pick one bound PHP domain for the post-cutover probe.

Phase B (cutover, SKIPPED when --dry-run):
  - stop, disable, and mask php<v>-fpm.service for every installed version.

Phase C (probe, SKIPPED when --dry-run):
  - HTTP GET http://127.0.0.1/jabali-healthcheck.php with the Host header set
    to each probe domain. Any non-200 triggers a full rollback (unmask +
    enable + start the global services).

Exits non-zero if preflight fails, if any probe fails, or if rollback itself
fails (a rollback failure is the only scenario that leaves the host half-cut).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}
			return runSliceCutover(cmd.Context(), dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "run preflight only; do not mask global FPM or probe")
	return cmd
}

// cutoverProbe is the bundle of facts needed to verify a user's FPM is serving
// PHP end-to-end after the global master is masked.
type cutoverProbe struct {
	Username string
	Domain   string // hostname for Host: header
}

func runSliceCutover(ctx context.Context, dryRun bool) error {
	// --- Phase A: preflight ---------------------------------------------------
	uRepo := userRepo()
	users, _, err := uRepo.List(ctx, repository.ListOptions{Limit: 10000})
	if err != nil {
		return fmt.Errorf("preflight: list users: %w", err)
	}

	var (
		probes     []cutoverProbe
		sliceFails []string
	)

	dRepo := domainRepoFromDB()
	for i := range users {
		u := users[i]
		// Admin users with no Linux username are not in scope.
		if u.Username == nil || *u.Username == "" {
			continue
		}
		name := *u.Username

		// a2: slice unit file exists + FPM service is active
		sliceUnit := fmt.Sprintf("/etc/systemd/system/jabali-user-%s.slice", name)
		if _, statErr := os.Stat(sliceUnit); statErr != nil {
			sliceFails = append(sliceFails, fmt.Sprintf("%s: missing slice unit %s", name, sliceUnit))
			continue
		}
		fpmSvc := fmt.Sprintf("jabali-fpm@%s.service", name)
		if !systemctlIsActive(fpmSvc) {
			sliceFails = append(sliceFails, fmt.Sprintf("%s: %s not active", name, fpmSvc))
			continue
		}

		// a4: pick a bound PHP domain for the probe. If a user has no
		// PHP-bound domain, we still cutover for them (their FPM is fine);
		// we just can't include them in phase C. Skipping silently is OK
		// because any domain they add later goes through the per-user path.
		domains, _, dErr := dRepo.ListByUserID(ctx, u.ID, repository.ListOptions{Limit: 100})
		if dErr != nil {
			sliceFails = append(sliceFails, fmt.Sprintf("%s: list domains failed: %v", name, dErr))
			continue
		}
		probe := pickProbeDomain(domains)
		if probe != "" {
			probes = append(probes, cutoverProbe{Username: name, Domain: probe})
		}
	}

	// a3: enumerate installed PHP versions
	versions, err := enumerateInstalledPHPVersions()
	if err != nil {
		return fmt.Errorf("preflight: enumerate PHP versions: %w", err)
	}
	if len(versions) == 0 {
		return fmt.Errorf("preflight: no PHP versions found under /etc/php/*/fpm/pool.d")
	}

	// a5: if any user failed, print and exit
	if len(sliceFails) > 0 {
		fmt.Fprintln(os.Stderr, "preflight failed — resolve these before cutover:")
		for _, f := range sliceFails {
			fmt.Fprintln(os.Stderr, "  -", f)
		}
		return fmt.Errorf("preflight failed: %d users not ready", len(sliceFails))
	}

	// a6: dry-run exit
	fmt.Printf("preflight OK: %d users ready (%d probe domains), %d PHP versions to mask: %s\n",
		len(users), len(probes), len(versions), strings.Join(versions, ", "))
	if dryRun {
		fmt.Println("--dry-run: stopping before cutover.")
		return nil
	}

	// --- Phase B: cutover ----------------------------------------------------
	fmt.Println("\n→ Phase B: stop/disable/mask global php<v>-fpm.service")
	start := time.Now()
	for _, v := range versions {
		svc := fmt.Sprintf("php%s-fpm.service", v)
		for _, verb := range []string{"stop", "disable", "mask"} {
			if out, vErr := runSystemctl(verb, svc); vErr != nil {
				// Rollback + abort
				fmt.Fprintf(os.Stderr, "  ✗ systemctl %s %s failed: %v\n    %s\n", verb, svc, vErr, out)
				return rollbackOrAbort(versions, fmt.Errorf("cutover aborted during %s %s", verb, svc))
			}
			fmt.Printf("  ✓ systemctl %s %s\n", verb, svc)
		}
	}

	// --- Phase C: probe ------------------------------------------------------
	fmt.Println("\n→ Phase C: probe jabali-healthcheck.php through nginx")
	var probeFails []string
	httpClient := &http.Client{Timeout: 10 * time.Second}
	for _, p := range probes {
		status, pErr := probeHealthcheck(ctx, httpClient, p.Domain)
		if pErr != nil {
			probeFails = append(probeFails, fmt.Sprintf("%s (%s): %v", p.Username, p.Domain, pErr))
			continue
		}
		if status != http.StatusOK {
			probeFails = append(probeFails, fmt.Sprintf("%s (%s): HTTP %d", p.Username, p.Domain, status))
			continue
		}
		fmt.Printf("  ✓ %s via %s: 200\n", p.Username, p.Domain)
	}
	if len(probeFails) > 0 {
		fmt.Fprintln(os.Stderr, "probe failed for:")
		for _, f := range probeFails {
			fmt.Fprintln(os.Stderr, "  -", f)
		}
		return rollbackOrAbort(versions, fmt.Errorf("cutover aborted after probe failures"))
	}

	// --- Phase D: finalize ---------------------------------------------------
	fmt.Printf("\n✓ cutover complete in %s. Global php-fpm services masked on this host.\n", time.Since(start).Round(time.Millisecond))
	return nil
}

// pickProbeDomain picks an enabled PHP-bound domain for the probe. Returns
// an empty string if the user has no PHP-bound enabled domain.
func pickProbeDomain(ds []models.Domain) string {
	// Deterministic pick: sort by name to avoid flaky probe target selection
	// on re-runs. First enabled + PHP-bound wins.
	sort.Slice(ds, func(i, j int) bool { return ds[i].Name < ds[j].Name })
	for _, d := range ds {
		if !d.IsEnabled {
			continue
		}
		if d.PHPPoolID == nil {
			continue
		}
		return d.Name
	}
	return ""
}

// enumerateInstalledPHPVersions scans /etc/php/<ver>/fpm/pool.d — the
// canonical marker that Sury installed php<ver>-fpm on this host.
func enumerateInstalledPHPVersions() ([]string, error) {
	entries, err := os.ReadDir("/etc/php")
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, sErr := os.Stat(filepath.Join("/etc/php", e.Name(), "fpm", "pool.d")); sErr != nil {
			if os.IsNotExist(sErr) {
				continue
			}
			if _, isPerm := sErr.(*fs.PathError); isPerm {
				return nil, sErr
			}
		}
		versions = append(versions, e.Name())
	}
	sort.Strings(versions)
	return versions, nil
}

// systemctlIsActive returns true when `systemctl is-active <unit>` exits 0.
func systemctlIsActive(unit string) bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", unit)
	return cmd.Run() == nil
}

// runSystemctl runs `systemctl <verb> <unit>` and returns the combined
// output for error reporting.
func runSystemctl(verb, unit string) ([]byte, error) {
	cmd := exec.Command("systemctl", verb, unit)
	return cmd.CombinedOutput()
}

// probeHealthcheck issues an HTTP GET to the loopback nginx with the domain
// as the Host header, hitting /jabali-healthcheck.php. Returns the status
// code or an error if the request couldn't be made.
func probeHealthcheck(ctx context.Context, c *http.Client, domain string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/jabali-healthcheck.php", nil)
	if err != nil {
		return 0, err
	}
	req.Host = domain
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// rollbackOrAbort unmasks, enables, and starts every version's global
// php-fpm service. Returns the original error if rollback succeeds, or a
// wrapped error that names rollback as the reason the host is now in a
// half-cut state.
func rollbackOrAbort(versions []string, cause error) error {
	fmt.Fprintln(os.Stderr, "\n→ rollback: unmask/enable/start global php-fpm")
	var rbFails []string
	for _, v := range versions {
		svc := fmt.Sprintf("php%s-fpm.service", v)
		// unmask is best-effort: if the service wasn't masked yet, we still
		// want enable + start to happen.
		_, _ = runSystemctl("unmask", svc)
		if out, err := runSystemctl("enable", svc); err != nil {
			rbFails = append(rbFails, fmt.Sprintf("enable %s: %v (%s)", svc, err, out))
			continue
		}
		if out, err := runSystemctl("start", svc); err != nil {
			rbFails = append(rbFails, fmt.Sprintf("start %s: %v (%s)", svc, err, out))
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ rolled back %s\n", svc)
	}
	if len(rbFails) > 0 {
		fmt.Fprintln(os.Stderr, "ROLLBACK FAILED; host may be in an inconsistent state:")
		for _, f := range rbFails {
			fmt.Fprintln(os.Stderr, "  -", f)
		}
		return fmt.Errorf("%w; rollback also failed (%d services)", cause, len(rbFails))
	}
	return cause
}
