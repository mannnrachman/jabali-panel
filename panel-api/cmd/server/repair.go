package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
