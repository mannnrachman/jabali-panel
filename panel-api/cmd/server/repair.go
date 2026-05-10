package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// jabali repair — self-heal subcommand for known recurring scars on a
// deployment host. Each repair encapsulates one detector + one fix; the
// detector reports whether the host is currently broken in that specific
// way, and the fix puts it back to a known-good state.
//
// New scars get a new repairStep entry. The list lives in this file so
// the truth of "what jabali knows how to fix automatically" is in one
// place reachable from `jabali repair --diagnose`.
//
// ADR-0077.

type repairStep struct {
	// id is the kebab-case selector exposed via flags (e.g. --git-ownership).
	id string

	// label is the human-readable line printed during diagnose / repair.
	label string

	// destructive=true repairs touch operator data (re-clone, rm -rf
	// node_modules, etc). They run only with --all + --yes, or with their
	// explicit --<id> flag. --auto skips them.
	destructive bool

	// detect returns (broken, detail, err). detail is a short string used
	// in the diagnose output. err means the detector itself blew up.
	detect func(ctx repairCtx) (bool, string, error)

	// fix mutates host state to clear the broken condition. Should be
	// idempotent — calling fix twice when not broken must be a no-op.
	fix func(ctx repairCtx) error
}

type repairCtx struct {
	repoDir     string
	serviceUser string
	yes         bool
}

func newRepairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Detect and fix known deployment-host issues",
		Long: `Run a series of detectors and (optionally) self-healing fixes for
recurring deployment-host issues. Useful when 'jabali update' fails or a
host is in a state that would block the next update.

Modes:
  jabali repair --diagnose       Report what is broken; change nothing.
  jabali repair --auto           Fix every safe (non-destructive) issue.
  jabali repair --all --yes      Fix everything, including destructive
                                  repairs (e.g. re-clone /opt/jabali-panel).
  jabali repair --<id> [...]     Fix one or more specific repairs by id;
                                  see --diagnose output for available ids.

Destructive repairs require either --all together with --yes, or the
specific --<id> flag together with --yes. Without --yes they prompt
interactively before touching anything irreversible.`,
		SilenceUsage: true,
		RunE:         runRepair,
	}
	cmd.Flags().Bool("diagnose", false, "Report broken conditions without fixing")
	cmd.Flags().Bool("auto", false, "Fix every non-destructive (safe) issue")
	cmd.Flags().Bool("all", false, "Fix every issue including destructive ones")
	cmd.Flags().Bool("yes", false, "Skip interactive confirmation for destructive repairs")
	for _, s := range repairSteps() {
		cmd.Flags().Bool(s.id, false, fmt.Sprintf("Fix only: %s", s.label))
	}
	return cmd
}

func runRepair(cmd *cobra.Command, args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("jabali repair must run as root (try: sudo jabali repair ...)")
	}

	ctx := repairCtx{
		repoDir:     envOr("JABALI_REPO_DIR", defaultRepoDir),
		serviceUser: envOr("JABALI_SERVICE_USER", "jabali"),
	}
	ctx.yes, _ = cmd.Flags().GetBool("yes")

	diagnose, _ := cmd.Flags().GetBool("diagnose")
	auto, _ := cmd.Flags().GetBool("auto")
	all, _ := cmd.Flags().GetBool("all")

	steps := repairSteps()

	// Pick which steps the operator selected.
	selected := map[string]bool{}
	anySpecific := false
	for _, s := range steps {
		if v, _ := cmd.Flags().GetBool(s.id); v {
			selected[s.id] = true
			anySpecific = true
		}
	}

	if !diagnose && !auto && !all && !anySpecific {
		// No flags = same as --diagnose. Defaulting to "do nothing" is
		// the safer choice than defaulting to "fix everything" for an
		// operator who typed `jabali repair` to see what it does.
		diagnose = true
		fmt.Println("(no mode flag — defaulting to --diagnose; pass --auto, --all, or --<id> to apply fixes)")
	}

	// Run every detector first. Even in fix mode, surfacing the full
	// list before mutating state lets the operator see the plan.
	type result struct {
		step   repairStep
		broken bool
		detail string
		err    error
	}
	var results []result
	for _, s := range steps {
		broken, detail, err := s.detect(ctx)
		results = append(results, result{s, broken, detail, err})
	}

	fmt.Println("Diagnostics:")
	anyBroken := false
	for _, r := range results {
		marker := "  ✓"
		state := "OK"
		switch {
		case r.err != nil:
			marker = "  !"
			state = fmt.Sprintf("detect failed: %v", r.err)
		case r.broken:
			marker = "  ✗"
			state = "BROKEN"
			if r.detail != "" {
				state = "BROKEN — " + r.detail
			}
			anyBroken = true
		}
		safety := ""
		if r.step.destructive {
			safety = " [destructive]"
		}
		fmt.Printf("%s [%s] %s%s\n     %s\n",
			marker, r.step.id, r.step.label, safety, state)
	}

	if diagnose {
		if !anyBroken {
			fmt.Println("\nNo issues detected.")
		} else {
			fmt.Println("\nRun `jabali repair --auto` to fix safe issues, or " +
				"`jabali repair --all --yes` to also apply destructive fixes.")
		}
		return nil
	}

	// Decide which steps to actually fix.
	toFix := []repairStep{}
	for _, r := range results {
		if r.err != nil || !r.broken {
			continue
		}
		switch {
		case anySpecific:
			if selected[r.step.id] {
				toFix = append(toFix, r.step)
			}
		case all:
			toFix = append(toFix, r.step)
		case auto:
			if !r.step.destructive {
				toFix = append(toFix, r.step)
			}
		}
	}

	if len(toFix) == 0 {
		if anyBroken {
			fmt.Println("\n(no repairs selected — pass --auto, --all, or --<id>)")
		} else {
			fmt.Println("\nNothing to fix.")
		}
		return nil
	}

	for _, s := range toFix {
		if s.destructive && !ctx.yes {
			ok, err := confirm(fmt.Sprintf("Apply destructive repair %q? This may overwrite host state.", s.id))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Printf("  [%s] skipped (declined)\n", s.id)
				continue
			}
		}
		fmt.Printf("\n→ [%s] %s\n", s.id, s.label)
		if err := s.fix(ctx); err != nil {
			return fmt.Errorf("repair %s: %w", s.id, err)
		}
		fmt.Printf("  ✓ %s applied\n", s.id)
	}

	fmt.Println("\n✓ Repair pass complete. Re-run `jabali update` to continue.")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func confirm(prompt string) (bool, error) {
	fmt.Printf("%s [y/N] ", prompt)
	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() {
		if err := scan.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	ans := strings.TrimSpace(strings.ToLower(scan.Text()))
	return ans == "y" || ans == "yes", nil
}

// repairSteps lists every known repair. Order matters: detectors run
// top-to-bottom, so cheap-and-blocking checks (.git pointer corruption)
// come before expensive ones (node_modules .bin/tsc check), and ownership
// fixes come before any check that would itself fail without ownership.
func repairSteps() []repairStep {
	return []repairStep{
		{
			id:          "git-pointer",
			label:       "/opt/jabali-panel/.git is a corrupted worktree pointer",
			destructive: true,
			detect:      detectGitPointer,
			fix:         fixGitPointer,
		},
		{
			id:    "git-ownership",
			label: "/opt/jabali-panel/.git owned by wrong user",
			detect: detectGitOwnership,
			fix:    fixGitOwnership,
		},
		{
			id:    "git-stale-worktrees",
			label: "/opt/jabali-panel/.git/worktrees has stale entries",
			detect: detectGitStaleWorktrees,
			fix:    fixGitStaleWorktrees,
		},
		{
			id:    "uploads-dir",
			label: "/var/lib/jabali-uploads missing or wrong perms",
			detect: detectUploadsDir,
			fix:    fixUploadsDir,
		},
		{
			id:    "ondrej-nginx-ppa",
			label: "stale ondrej/nginx PPA in apt sources (404 on noble)",
			detect: detectOndrejPPA,
			fix:    fixOndrejPPA,
		},
		{
			id:          "node-modules",
			label:       "panel-ui/node_modules partial (missing .bin/tsc)",
			destructive: true,
			detect:      detectNodeModules,
			fix:         fixNodeModules,
		},
		{
			id:    "daemon-reload",
			label: "systemd has unloaded unit-file changes on disk",
			detect: detectDaemonReload,
			fix:    fixDaemonReload,
		},
		{
			id:    "orphan-slices",
			label: "jabali-user-*.slice units exist for deleted unix users",
			detect: detectOrphanSlices,
			fix:    fixOrphanSlices,
		},
		{
			id:    "crowdsec-bouncer-key",
			label: "crowdsec-firewall-bouncer crash-loops with stale LAPI key",
			detect: detectCrowdSecBouncerKey,
			fix:    fixCrowdSecBouncerKey,
		},
		{
			id:    "apparmor-profiles-missing",
			label: "jabali AppArmor profiles absent from /etc/apparmor.d/",
			detect: detectAppArmorProfilesMissing,
			fix:    fixAppArmorProfilesMissing,
		},
		{
			id:    "apparmor-profiles-disabled",
			label: "jabali AppArmor profiles exist but are disabled",
			detect: detectAppArmorProfilesDisabled,
			fix:    fixAppArmorProfilesDisabled,
		},
	}
}

// ---------- git-pointer ----------
//
// Symptom: `.git` is a one-line FILE containing `gitdir: <abspath>` rather
// than a directory. Happens when an operator copies a worktree's `.git`
// pointer instead of the real repo, or when a partial rsync from a dev
// box's worktree lands those bytes on the deploy host. Result: every git
// command on the host fails with
//   fatal: not a git repository: <abspath-on-source-machine>
// and `jabali update` dies on the very first `git fetch`.

func detectGitPointer(ctx repairCtx) (bool, string, error) {
	gitPath := filepath.Join(ctx.repoDir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, fmt.Sprintf("%s does not exist", gitPath), nil
		}
		return false, "", err
	}
	if info.IsDir() {
		return false, "", nil
	}
	// Not a directory — read the contents. A worktree pointer file
	// is one short line `gitdir: <abspath>`.
	b, err := os.ReadFile(gitPath)
	if err != nil {
		return false, "", err
	}
	content := strings.TrimSpace(string(b))
	if strings.HasPrefix(content, "gitdir:") {
		return true, "pointer file → " + strings.TrimSpace(strings.TrimPrefix(content, "gitdir:")), nil
	}
	return true, "non-directory non-pointer", nil
}

func fixGitPointer(ctx repairCtx) error {
	// Re-clone /opt/jabali-panel from origin while preserving operator
	// state that is intentionally NOT in git: node_modules, .cache,
	// .env, bin/. This mirrors the recovery snippet from the runbook.
	repo := ctx.repoDir
	backup := repo + ".broken"

	originURL, err := readOriginURL(repo)
	if err != nil {
		return fmt.Errorf("could not determine remote origin URL: %w", err)
	}

	// Move broken tree out of the way.
	if _, err := os.Stat(backup); err == nil {
		if err := run("", "rm", "-rf", backup); err != nil {
			return fmt.Errorf("clean stale %s: %w", backup, err)
		}
	}
	if err := run("", "mv", repo, backup); err != nil {
		return err
	}

	// Fresh clone.
	if err := run("", "git", "clone", originURL, repo); err != nil {
		return err
	}
	if err := run("", "chown", "-R",
		ctx.serviceUser+":"+ctx.serviceUser, repo); err != nil {
		return err
	}

	// Restore preserved untracked state. Each cp is best-effort so a
	// missing source from the broken tree doesn't abort the recovery.
	preserves := []string{
		".env",
		"panel-ui/node_modules",
		"panel-ui/dist",
		".cache",
		"bin",
	}
	for _, p := range preserves {
		src := filepath.Join(backup, p)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(repo, p)
		_ = run("", "cp", "-a", src, dst)
	}

	fmt.Printf("  (preserved tree kept at %s — delete after you confirm the new clone works)\n", backup)
	return nil
}

func readOriginURL(repo string) (string, error) {
	c := exec.Command("git", "-C", repo, "remote", "get-url", "origin")
	out, err := c.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	// Fallback: read .git/config directly. Useful when the gitdir pointer
	// is broken but .git/config still exists somewhere reachable.
	cfg, cerr := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if cerr != nil {
		// Last resort: try the broken-clone backup if we already moved it.
		cfg, cerr = os.ReadFile(filepath.Join(repo+".broken", ".git", "config"))
	}
	if cerr != nil {
		return "", err
	}
	for _, line := range strings.Split(string(cfg), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "url = ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "url = ")), nil
		}
	}
	return "", fmt.Errorf("origin URL not found in any git config")
}

// ---------- git-ownership ----------
//
// Symptom: `.git/objects/*` or `.git/FETCH_HEAD` is owned by root after
// a hand-run `git fetch` as root, so the next `jabali update` (which
// runs git as the jabali user) hits "permission denied" or
// "fatal: detected dubious ownership".

func detectGitOwnership(ctx repairCtx) (bool, string, error) {
	gitDir := filepath.Join(ctx.repoDir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", nil // git-pointer detector handles missing dir
		}
		return false, "", err
	}
	if !info.IsDir() {
		return false, "", nil // git-pointer detector owns this case
	}
	out, err := exec.Command("stat", "-c", "%U", gitDir).Output()
	if err != nil {
		return false, "", err
	}
	owner := strings.TrimSpace(string(out))
	if owner != ctx.serviceUser {
		return true, fmt.Sprintf("owner=%s expected=%s", owner, ctx.serviceUser), nil
	}
	return false, "", nil
}

func fixGitOwnership(ctx repairCtx) error {
	return run("", "chown", "-R",
		ctx.serviceUser+":"+ctx.serviceUser,
		filepath.Join(ctx.repoDir, ".git"))
}

// ---------- git-stale-worktrees ----------
//
// Symptom: `.git/worktrees/<name>/gitdir` files reference paths from a
// dev machine that don't exist on the deploy host. Git itself ignores
// missing worktrees on most operations, but `git worktree prune --expire
// now` keeps the dir clean and removes any stale config that could
// confuse downstream tooling.

func detectGitStaleWorktrees(ctx repairCtx) (bool, string, error) {
	wtRoot := filepath.Join(ctx.repoDir, ".git", "worktrees")
	entries, err := os.ReadDir(wtRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", nil
		}
		return false, "", err
	}
	if len(entries) == 0 {
		return false, "", nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return true, strings.Join(names, ","), nil
}

func fixGitStaleWorktrees(ctx repairCtx) error {
	// `git worktree prune --expire now` is the supported way to drop
	// every worktree subdir whose checkout path is missing — exactly
	// the case a deploy host hits.
	return run(ctx.repoDir, "git", "worktree", "prune", "--expire", "now")
}

// ---------- uploads-dir ----------
//
// Symptom: /var/lib/jabali-uploads missing → file uploads fail with
// "no such file or directory". The dir is created in install.sh on
// fresh installs but partial state can leave it absent.

func detectUploadsDir(ctx repairCtx) (bool, string, error) {
	const dir = "/var/lib/jabali-uploads"
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, "missing", nil
		}
		return false, "", err
	}
	if !info.IsDir() {
		return true, "exists but not a directory", nil
	}
	return false, "", nil
}

func fixUploadsDir(_ repairCtx) error {
	const dir = "/var/lib/jabali-uploads"
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// Owner: root:jabali (panel-api writes via systemd ReadWritePaths,
	// jabali-agent reads to ingest). Ownership matches install.sh.
	return run("", "chown", "root:jabali", dir)
}

// ---------- ondrej-nginx-ppa ----------
//
// Symptom: apt update/install fails on Debian noble because the legacy
// ondrej/nginx PPA returns 404. Same scar as install.sh's strip step.

func detectOndrejPPA(_ repairCtx) (bool, string, error) {
	candidates := []string{
		"/etc/apt/sources.list.d/ondrej-ubuntu-nginx-noble.sources",
		"/etc/apt/sources.list.d/ondrej-ubuntu-nginx-noble.list",
		"/etc/apt/sources.list.d/ondrej-nginx.list",
	}
	var found []string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			found = append(found, filepath.Base(p))
		}
	}
	if len(found) == 0 {
		return false, "", nil
	}
	return true, strings.Join(found, ","), nil
}

func fixOndrejPPA(_ repairCtx) error {
	return run("", "bash", "-c",
		"rm -f /etc/apt/sources.list.d/ondrej-ubuntu-nginx-noble.sources "+
			"/etc/apt/sources.list.d/ondrej-ubuntu-nginx-noble.list "+
			"/etc/apt/sources.list.d/ondrej-nginx.list")
}

// ---------- node-modules ----------
//
// Symptom: `panel-ui/node_modules/.bin/tsc` is missing — npm ci reported
// success but produced a partial install (or got interrupted). Re-running
// `jabali update` would surface the same scar; the repair wipes node_modules
// so the next update / build starts from a known-clean state.

func detectNodeModules(ctx repairCtx) (bool, string, error) {
	tsc := filepath.Join(ctx.repoDir, "panel-ui", "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err == nil {
		return false, "", nil
	}
	// Only flag broken if the lockfile is present — a fresh checkout
	// without npm ci yet is not a "repair" case.
	lock := filepath.Join(ctx.repoDir, "panel-ui", "package-lock.json")
	if _, err := os.Stat(lock); err != nil {
		return false, "", nil
	}
	return true, "node_modules/.bin/tsc missing despite package-lock.json", nil
}

func fixNodeModules(ctx repairCtx) error {
	nm := filepath.Join(ctx.repoDir, "panel-ui", "node_modules")
	return run("", "rm", "-rf", nm)
}

// ---------- daemon-reload ----------
//
// Symptom: systemd is running with an old version of one of jabali's
// unit files because someone installed a new version on disk but never
// ran `systemctl daemon-reload`. systemctl itself surfaces this via the
// per-unit `NeedDaemonReload` property when set to "yes".

func detectDaemonReload(_ repairCtx) (bool, string, error) {
	out, err := exec.Command("bash", "-c",
		"systemctl list-units --all --no-legend 'jabali-*.service' 'jabali-*.timer' 'jabali-*.slice' "+
			"| awk '{print $1}'").Output()
	if err != nil {
		return false, "", err
	}
	var stale []string
	for _, line := range strings.Split(string(out), "\n") {
		unit := strings.TrimSpace(line)
		if unit == "" {
			continue
		}
		propOut, err := exec.Command("systemctl", "show", unit, "-p", "NeedDaemonReload").Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(propOut), "NeedDaemonReload=yes") {
			stale = append(stale, unit)
		}
	}
	if len(stale) == 0 {
		return false, "", nil
	}
	return true, strings.Join(stale, ","), nil
}

func fixDaemonReload(_ repairCtx) error {
	return run("", "systemctl", "daemon-reload")
}

// ---------- orphan-slices ----------
//
// Symptom: jabali-user-<username>.slice units linger after the unix user was
// deleted (e.g. deleted before slice teardown was wired into userDeleteHandler).
// Orphan slices consume cgroup resources and clutter `jabali server-status`.

func orphanSliceUsernames() ([]string, error) {
	out, err := exec.Command("systemctl", "list-units", "--all", "--no-legend", "jabali-user-*.slice").Output()
	if err != nil {
		return nil, err
	}
	var orphans []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		unit := fields[0]
		if !strings.HasPrefix(unit, "jabali-user-") || !strings.HasSuffix(unit, ".slice") {
			continue
		}
		username := strings.TrimSuffix(strings.TrimPrefix(unit, "jabali-user-"), ".slice")
		if _, err := user.Lookup(username); err != nil {
			orphans = append(orphans, username)
		}
	}
	return orphans, nil
}

func detectOrphanSlices(_ repairCtx) (bool, string, error) {
	orphans, err := orphanSliceUsernames()
	if err != nil {
		return false, "", err
	}
	if len(orphans) == 0 {
		return false, "", nil
	}
	return true, strings.Join(orphans, ","), nil
}

func fixOrphanSlices(_ repairCtx) error {
	orphans, err := orphanSliceUsernames()
	if err != nil {
		return err
	}
	for _, username := range orphans {
		fmt.Printf("  removing orphan slice: %s\n", username)
		_ = exec.Command("systemctl", "stop", "jabali-fpm@"+username+".service").Run()
		_ = exec.Command("systemctl", "disable", "jabali-fpm@"+username+".service").Run()
		_ = exec.Command("systemctl", "stop", "jabali-user-"+username+".slice").Run()
		sliceUnit := "/etc/systemd/system/jabali-user-" + username + ".slice"
		fpmDropinDir := "/etc/systemd/system/jabali-fpm@" + username + ".service.d"
		fpmDropin := fpmDropinDir + "/slice.conf"
		_ = os.Remove(sliceUnit)
		_ = os.Remove(fpmDropin)
		_ = os.Remove(fpmDropinDir) // rmdir; no-ops if non-empty or absent
	}
	if len(orphans) > 0 {
		return run("", "systemctl", "daemon-reload")
	}
	return nil
}

// ---------- crowdsec-bouncer-key ----------
//
// Symptom: crowdsec-firewall-bouncer service loops in failed/activating state.
// Journal shows "bouncer stream halted" or "Unauthorized". Root cause: the LAPI
// database was reset or the installer re-seeded a new key, but the bouncer YAML
// still carries the old key. The fix mirrors install.sh: delete the stale LAPI
// registration, add a fresh one, patch the YAML, and restart the service.

func crowdsecFirewallBouncerService() string {
	for _, pkg := range []string{
		"crowdsec-firewall-bouncer-nftables",
		"crowdsec-firewall-bouncer-iptables",
	} {
		out, err := exec.Command("systemctl", "cat", pkg+".service").Output()
		if err == nil && len(out) > 0 {
			return pkg + ".service"
		}
	}
	return ""
}

func detectCrowdSecBouncerKey(_ repairCtx) (bool, string, error) {
	if _, err := exec.LookPath("cscli"); err != nil {
		return false, "", nil // crowdsec not installed
	}
	svc := crowdsecFirewallBouncerService()
	if svc == "" {
		return false, "", nil // bouncer package not installed
	}
	out, _ := exec.Command("systemctl", "is-active", svc).Output()
	if strings.TrimSpace(string(out)) == "active" {
		return false, "", nil // running fine
	}
	// Only flag if the service is failed/activating (crash-loop). A service
	// that was never started intentionally (inactive/disabled) is not ours
	// to repair here.
	subOut, _ := exec.Command("systemctl", "show", svc, "-p", "SubState").Output()
	sub := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(subOut)), "SubState="))
	if sub != "failed" && sub != "auto-restart" {
		return false, "", nil
	}
	// Look for stale-key evidence in recent journal lines.
	journal, _ := exec.Command("journalctl", "-u", svc, "-n", "80", "--no-pager").Output()
	j := string(journal)
	if strings.Contains(j, "stream halted") || strings.Contains(j, "Unauthorized") {
		return true, fmt.Sprintf("%s crash-loop: stale LAPI key detected", svc), nil
	}
	return true, fmt.Sprintf("%s SubState=%s", svc, sub), nil
}

func fixCrowdSecBouncerKey(_ repairCtx) error {
	svc := crowdsecFirewallBouncerService()
	if svc == "" {
		return fmt.Errorf("crowdsec-firewall-bouncer service not found")
	}
	pkg := strings.TrimSuffix(svc, ".service")
	conf := "/etc/crowdsec/bouncers/" + pkg + ".yaml"
	if _, err := os.Stat(conf); err != nil {
		return fmt.Errorf("bouncer conf %s: %w", conf, err)
	}
	const bouncerName = "jabali-firewall"
	// Prune stale LAPI registration (ignore error if it never existed).
	_ = exec.Command("cscli", "bouncers", "delete", bouncerName).Run()
	// Mint a fresh key.
	keyOut, err := exec.Command("cscli", "bouncers", "add", bouncerName, "-o", "raw").Output()
	if err != nil {
		return fmt.Errorf("cscli bouncers add: %w", err)
	}
	apiKey := strings.TrimSpace(string(keyOut))
	if apiKey == "" {
		return fmt.Errorf("cscli bouncers add returned empty key")
	}
	// Patch api_key in the bouncer YAML.
	if err := run("", "yq", "-y", "-i",
		fmt.Sprintf(`.api_key = "%s"`, apiKey), conf); err != nil {
		return fmt.Errorf("yq patch %s: %w", conf, err)
	}
	// Restart with fresh credentials.
	return run("", "systemctl", "restart", svc)
}

// ---------- apparmor-profiles-missing ----------
//
// Symptom: the five jabali AppArmor profiles are absent from
// /etc/apparmor.d/.  This happens when install_apparmor ran before
// clone_or_update_repo (ordering bug fixed 2026-05-10) or when the
// profiles were accidentally removed.  Fix: copy profiles from the
// repo and load them in complain mode.

var jabaliAAProfiles = []string{
	"usr.local.bin.jabali-panel-api",
	"usr.local.bin.jabali-agent",
	"usr.local.bin.jabali-bulwark",
	"usr.local.bin.jabali-kratos",
	"usr.local.bin.stalwart-mail",
}

func detectAppArmorProfilesMissing(ctx repairCtx) (bool, string, error) {
	if _, err := exec.LookPath("aa-status"); err != nil {
		return false, "", nil // AppArmor not installed
	}
	missing := []string{}
	for _, p := range jabaliAAProfiles {
		if _, err := os.Stat("/etc/apparmor.d/" + p); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return false, "", nil
	}
	return true, fmt.Sprintf("%d jabali AppArmor profile(s) missing: %s",
		len(missing), strings.Join(missing, ", ")), nil
}

func fixAppArmorProfilesMissing(ctx repairCtx) error {
	srcDir := filepath.Join(ctx.repoDir, "install", "apparmor")
	if _, err := os.Stat(srcDir); err != nil {
		return fmt.Errorf("AppArmor profile source dir %s not found: %w", srcDir, err)
	}
	for _, p := range jabaliAAProfiles {
		src := filepath.Join(srcDir, p)
		dst := "/etc/apparmor.d/" + p
		if _, err := os.Stat(src); err != nil {
			continue // profile not in this repo version — skip
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		// Load in complain mode (non-blocking for the running process).
		_ = exec.Command("apparmor_parser", "-r", dst).Run()
		_ = exec.Command("aa-complain", dst).Run()
	}
	return nil
}

// ---------- apparmor-profiles-disabled ----------
//
// Symptom: profiles exist under /etc/apparmor.d/ but aa-disable has
// created symlinks in /etc/apparmor.d/disable/ — the profiles are on
// disk but not enforced.  Fix: remove the disable symlinks and reload
// in complain mode.

func detectAppArmorProfilesDisabled(_ repairCtx) (bool, string, error) {
	if _, err := exec.LookPath("aa-status"); err != nil {
		return false, "", nil
	}
	disabled := []string{}
	for _, p := range jabaliAAProfiles {
		disablePath := "/etc/apparmor.d/disable/" + p
		if _, err := os.Lstat(disablePath); err == nil {
			disabled = append(disabled, p)
		}
	}
	if len(disabled) == 0 {
		return false, "", nil
	}
	return true, fmt.Sprintf("%d jabali AppArmor profile(s) disabled: %s",
		len(disabled), strings.Join(disabled, ", ")), nil
}

func fixAppArmorProfilesDisabled(_ repairCtx) error {
	for _, p := range jabaliAAProfiles {
		disablePath := "/etc/apparmor.d/disable/" + p
		profilePath := "/etc/apparmor.d/" + p
		if _, err := os.Lstat(disablePath); err != nil {
			continue
		}
		if err := os.Remove(disablePath); err != nil {
			return fmt.Errorf("remove disable symlink %s: %w", disablePath, err)
		}
		if _, err := os.Stat(profilePath); err == nil {
			_ = exec.Command("apparmor_parser", "-r", profilePath).Run()
			_ = exec.Command("aa-complain", profilePath).Run()
		}
	}
	return nil
}

// repairHint is appended to error messages from runUpdate so an operator
// who hits a wall has a clear next move: a single command that may
// self-heal whatever broke the update.
//
// Wired into update.go's error-path returns. Cheap to produce — no IO.
func repairHint() string {
	return "\n  → If this looks like a deployment-host issue, try:\n" +
		"      jabali repair --diagnose\n" +
		"      jabali repair --auto\n"
}
