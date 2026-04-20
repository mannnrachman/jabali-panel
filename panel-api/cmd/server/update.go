package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// Paths match install.sh defaults. The binary can't replace itself while
// running, so we build to a temp path, install, then trigger a restart.
const (
	defaultRepoDir      = "/opt/jabali-panel"
	defaultPanelBinPath = "/usr/local/bin/jabali-panel"
	defaultAgentBinPath = "/usr/local/bin/jabali-agent"
	defaultGoRoot       = "/usr/local/go"
)

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Pull latest code, rebuild, migrate, and restart services",
		Long: `Performs a self-update:
  1. git fetch + hard-reset to origin/main. Local tracked-file drift is
     discarded (the VM is a deployment target — origin is authoritative);
     untracked files like node_modules, .env, bin/, .cache/ are preserved.
     Any discarded drift is printed as a diffstat so it's visible; recover
     via git reflog if needed.
  2. If HEAD moved: npm ci, vite build, go build (panel-api + panel-agent),
     install new binaries, run pending migrations, restart services
  3. If HEAD did not move: print "Already up to date" and exit. Use
     --force (-f) to run the rebuild + restart cycle anyway, e.g. to
     recover from a previous update that failed partway through.`,
		RunE: runUpdate,
	}
	cmd.Flags().BoolP("force", "f", false,
		"Run the full rebuild/restart cycle even when git pull found no new commits")
	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	// Must run as root to install binaries and restart services.
	if os.Geteuid() != 0 {
		return fmt.Errorf("jabali update must run as root (try: sudo jabali-panel update)")
	}

	repoDir := os.Getenv("JABALI_REPO_DIR")
	if repoDir == "" {
		repoDir = defaultRepoDir
	}

	goRoot := os.Getenv("JABALI_GO_ROOT")
	if goRoot == "" {
		goRoot = defaultGoRoot
	}
	goBin := goRoot + "/bin/go"

	// The repo is owned by the jabali service user. Run git/npm/go as
	// that user to avoid git's "dubious ownership" check and to keep
	// node_modules/go-cache owned correctly.
	serviceUser := os.Getenv("JABALI_SERVICE_USER")
	if serviceUser == "" {
		serviceUser = "jabali"
	}

	asUser := func(dir string, name string, args ...string) error {
		allArgs := append([]string{"-u", serviceUser, "-H", "env",
			"HOME=" + repoDir,
			"PATH=" + goRoot + "/bin:/usr/bin:/bin",
			"GOCACHE=" + repoDir + "/.cache/go-build",
			"GOMODCACHE=" + repoDir + "/.cache/go-mod",
		}, name)
		allArgs = append(allArgs, args...)
		return run(dir, "sudo", allArgs...)
	}

	force, _ := cmd.Flags().GetBool("force")

	// gitHead captures HEAD as a string so we can detect whether
	// `git pull` actually advanced the branch. The git-pull step
	// populates preHEAD before the pull, postHEAD after — when they
	// match and --force wasn't passed, we skip the expensive build
	// + restart steps below.
	//
	// Runs via `sudo -u <serviceUser>` because the repo is owned by
	// the jabali user; git 2.35+ refuses to operate on a repo owned
	// by a different uid ("fatal: detected dubious ownership"),
	// which surfaces as exit 128. Mirrors the asUser helper above
	// but captures stdout for the SHA instead of inheriting it.
	var preHEAD, postHEAD string
	gitHead := func() (string, error) {
		c := exec.Command("sudo", "-u", serviceUser,
			"git", "-C", repoDir, "rev-parse", "HEAD")
		out, err := c.Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	// Prelude steps — always run. Ownership self-heal, then git pull
	// with before/after HEAD capture so we can decide whether to
	// continue past this point.
	prelude := []struct {
		name string
		fn   func() error
	}{
		{"fix repo ownership", func() error {
			// `git pull` runs as the jabali user, but a previous hand-run
			// of `git fetch`/`git pull` as root inside the repo silently
			// chowns FETCH_HEAD/ORIG_HEAD/objects/* to root — and the
			// next `jabali update` then dies with "cannot open
			// '.git/FETCH_HEAD': Permission denied". Re-chown the .git
			// dir before pulling so the update self-heals instead of
			// requiring the operator to know the magic chown command.
			//
			// Scope intentionally narrow: just .git/, not the whole
			// repo (we don't want to clobber node_modules or other
			// trees that might legitimately be group-owned differently).
			return run("", "chown", "-R",
				serviceUser+":"+serviceUser, repoDir+"/.git")
		}},
		{"git fetch + reset to origin/main", func() error {
			// The VM is a deployment target, not a source of truth. Tracked-
			// file drift (typical cause: operator `sed`/patches a file in
			// place on the VM to test a fix, then later commits the same
			// change from a dev box and pushes) is ALWAYS disposable on
			// update — the authoritative copy is origin/main. Using
			// `git pull --ff-only` fails loudly in this case and forces the
			// operator to hand-stash or hand-reset before the update can
			// proceed; switching to fetch + `reset --hard origin/main`
			// makes the update self-healing without clobbering anything
			// the operator would actually want to keep.
			//
			// Untracked files (node_modules, bin/, .env, .cache/) are
			// untouched by `reset --hard` — it only rewrites tracked
			// content. Be LOUD about discarded drift so an operator who
			// didn't expect it sees what's gone and can recover it from
			// reflog if needed.
			pre, err := gitHead()
			if err != nil {
				return err
			}
			preHEAD = pre
			if err := asUser(repoDir, "git", "fetch", "origin", "main"); err != nil {
				return err
			}
			// Show diffstat of any local drift vs HEAD before we reset so
			// the operator can see what was clobbered. Silent on clean tree.
			_ = asUser(repoDir, "bash", "-c",
				`d=$(git diff --stat HEAD); `+
					`if [ -n "$d" ]; then `+
					`  echo "  (discarding local modifications on deployment target:)"; `+
					`  echo "$d" | sed "s/^/    /"; `+
					`  echo "  (recover from reflog if this was a surprise: git reflog, git reset --hard <sha>)"; `+
					`fi`)
			if err := asUser(repoDir, "git", "reset", "--hard", "origin/main"); err != nil {
				return err
			}
			post, err := gitHead()
			if err != nil {
				return err
			}
			postHEAD = post
			return nil
		}},
	}

	// Build/apply steps — run only when HEAD moved OR --force was passed.
	// Keep in lockstep with the install.sh counterparts they mirror.
	buildSteps := []struct {
		name string
		fn   func() error
	}{
		{"install deps", func() error {
			// Re-run the install script's dependency functions for any
			// new packages added since the last update.
			return run("", "bash", "-c",
				"export DEBIAN_FRONTEND=noninteractive && "+
					"apt-get install -y -qq --no-install-recommends nginx >/dev/null 2>&1; "+
					"install -d -m 0755 /etc/nginx/sites-available; "+
					"install -d -m 0755 /etc/nginx/sites-enabled; "+
					"true")
		}},
		{"sync systemd + shims", func() error {
			// install.sh copies these on first install; the update path
			// needs to re-copy them so unit file / shim changes land.
			// Keep in sync with install_jabali_slices() in install.sh.
			return run("", "bash", "-c",
				"set -e; "+
					"install -d -m 0755 /usr/local/libexec/jabali; "+
					"install -m 0755 "+repoDir+"/install/systemd/fpm-pre-start /usr/local/libexec/jabali/fpm-pre-start; "+
					"install -m 0755 "+repoDir+"/install/systemd/fpm-exec /usr/local/libexec/jabali/fpm-exec; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali.slice /etc/systemd/system/jabali.slice; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali-user.slice /etc/systemd/system/jabali-user.slice; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali-fpm@.service /etc/systemd/system/jabali-fpm@.service; "+
					"systemctl daemon-reload")
		}},
		{"sync static assets", func() error {
			// Mirror the file-writing half of install_php_pool_template(),
			// ensure_user_and_dirs(), and install_phpmyadmin() from
			// install.sh so template, group-membership, and phpMyAdmin
			// handler changes land on update. apt package installs and
			// systemd service creation stay in install.sh — update is for
			// hosts that already booted once.
			return run("", "bash", "-c",
				"set -e; "+
					"install -d -m 0755 -o root -g root /etc/jabali-panel/fpm; "+
					"install -d -m 0755 -o root -g root /etc/jabali-panel/user-phpver; "+
					// pool template — changes here affect every future
					// pool-apply for every user.
					"install -m 0644 "+repoDir+"/install/php/jabali-php-pool.conf.tmpl /etc/jabali-panel/php-pool.conf.tmpl; "+
					// phpMyAdmin SSO handler — install.sh extracts the
					// tarball to /opt/phpmyadmin/current/, but updates to
					// sso.php/config.inc.php shipped in the repo never
					// reach the host unless install.sh is re-run. Copy
					// them now so update.go is the single-source refresh.
					"if [ -d /opt/phpmyadmin/current ]; then "+
					"  install -m 0640 -o root -g www-data "+repoDir+"/install/phpmyadmin/sso.php /opt/phpmyadmin/current/sso.php; "+
					// Strip controluser/controlpass/controlhost/controlport
					// from an existing config.inc.php. Earlier installs
					// seeded controluser='root', which makes phpMyAdmin
					// open a second connection as root@localhost on every
					// page load and surface two "Access denied" banners
					// even though SSO succeeded. pmadb is already false,
					// so no control connection is needed — stripping the
					// keys makes PMA skip it. Idempotent: sed -i only
					// rewrites if lines match, so re-running is a no-op
					// once they're gone.
					"  if [ -f /opt/phpmyadmin/current/config.inc.php ]; then "+
					"    sed -i \"/\\$cfg\\['Servers'\\]\\[1\\]\\['control\\(user\\|pass\\|host\\|port\\)'\\]/d\" /opt/phpmyadmin/current/config.inc.php; "+
					"  fi; "+
					"fi; "+
					// M11 filebrowser decommission (ADR-0030): stop/disable the
					// legacy service + strip its nginx include so updates don't
					// resurrect dead bits. Idempotent: || true swallows
					// "not-found" on hosts that never had it.
					"systemctl stop jabali-filebrowser.service 2>/dev/null || true; "+
					"systemctl disable jabali-filebrowser.service 2>/dev/null || true; "+
					"rm -f /etc/systemd/system/jabali-filebrowser.service /etc/nginx/conf.d/jabali-files.conf /etc/nginx/sites-available/includes/jabali-files.conf; "+
					"sed -i '/includes\\/jabali-files.conf/d' /etc/nginx/sites-available/jabali-default.conf 2>/dev/null || true; "+
					"systemctl daemon-reload; "+
					"nginx -t && systemctl reload nginx; "+
					// SFTP jabali-sftp group — required by the sshd Match block and by
					// the reconciler's join_sftp_group agent call. Idempotent.
					"getent group jabali-sftp >/dev/null || groupadd --system jabali-sftp; "+
					// SFTP sshd drop-in — update ships new versions of the Match block.
					// Idempotent: install -m 0644 is a no-op if target matches source.
					"install -m 0644 -o root -g root "+repoDir+"/install/ssh/jabali-sftp.conf /etc/ssh/sshd_config.d/jabali-sftp.conf; "+
					// Validate before reload so a broken config doesn't take down SSH.
					// If sshd -t fails here, the step returns non-zero and the update
					// halts before the reload. Operator sees the error and fixes the
					// source file.
					"sshd -t; "+
					"systemctl reload sshd; "+
					// jabali service user in www-data group — needed for
					// the reconciler's per-user FPM socket stat-check.
					// usermod is idempotent; 'groups | grep -w' avoids an
					// unnecessary write when already a member.
					"groups "+serviceUser+" | grep -qw www-data || usermod -aG www-data "+serviceUser)
		}},
		{"npm ci", func() error {
			// Wipe node_modules before npm ci. npm ci's docs promise it
			// does this itself, but in practice it dies with
			//   ENOTEMPTY: directory not empty, rmdir '.../node_modules/vite'
			// whenever a prior partial install or filesystem quirk leaves
			// a half-removed package tree behind. Doing the rm ourselves
			// is the canonical workaround — safe here because node_modules
			// is fully regenerated from package-lock on every run.
			return asUser(repoDir+"/panel-ui", "bash", "-c",
				"rm -rf node_modules && npm ci --no-audit --no-fund")
		}},
		{"build frontend", func() error {
			return asUser(repoDir+"/panel-ui", "npm", "run", "build")
		}},
		{"build panel-api", func() error {
			return asUser(repoDir, goBin, "build", "-trimpath", "-ldflags", "-s -w",
				"-o", repoDir+"/bin/jabali-panel.new", "./panel-api/cmd/server")
		}},
		{"build panel-agent", func() error {
			return asUser(repoDir, goBin, "build", "-trimpath", "-ldflags", "-s -w",
				"-o", repoDir+"/bin/jabali-agent.new", "./panel-agent/cmd/jabali-agent")
		}},
		{"install binaries", func() error {
			if err := run("", "install", "-m", "0755", repoDir+"/bin/jabali-panel.new", defaultPanelBinPath); err != nil {
				return err
			}
			if err := run("", "install", "-m", "0755", repoDir+"/bin/jabali-agent.new", defaultAgentBinPath); err != nil {
				return err
			}
			// Idempotent ergonomic alias: `jabali` → `jabali-panel`.
			// install.sh creates this on fresh installs; update.go refreshes it
			// on every upgrade in case it got clobbered.
			_ = run("", "ln", "-sf", defaultPanelBinPath, "/usr/local/bin/jabali")
			_ = os.Remove(repoDir + "/bin/jabali-panel.new")
			_ = os.Remove(repoDir + "/bin/jabali-agent.new")
			return nil
		}},
		{"run migrations", func() error {
			return run("", defaultPanelBinPath, "migrate", "up")
		}},
		{"restart services", func() error {
			if err := run("", "systemctl", "restart", "jabali-agent"); err != nil {
				return err
			}
			return run("", "systemctl", "restart", "jabali-panel")
		}},
	}

	for _, s := range prelude {
		fmt.Printf("→ %s\n", s.name)
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}

	// Fast path: git pull reported no new commits. The build + restart
	// cycle would do ~30-60 s of CPU work + bounce services, all for a
	// no-op. Skip unless the operator asked for a forced rebuild.
	if preHEAD == postHEAD && !force {
		shortSHA := preHEAD
		if len(shortSHA) >= 7 {
			shortSHA = shortSHA[:7]
		}
		fmt.Printf("\n✓ Already up to date (HEAD=%s). Skipped rebuild.\n", shortSHA)
		fmt.Println("  Run `jabali update --force` to rebuild and restart anyway.")
		return nil
	}

	for _, s := range buildSteps {
		fmt.Printf("→ %s\n", s.name)
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}

	fmt.Println("\n✓ Update complete.")
	return nil
}

// run executes a command with inherited stdout/stderr so the user sees
// build output and errors in real time.
func run(dir string, name string, args ...string) error {
	c := exec.Command(name, args...)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	// Inherit PATH + GOPATH etc but ensure our Go is first.
	c.Env = appendGoPath(os.Environ())
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func appendGoPath(env []string) []string {
	goRoot := os.Getenv("JABALI_GO_ROOT")
	if goRoot == "" {
		goRoot = defaultGoRoot
	}
	// Prepend Go bin to PATH so the right `go` is found.
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + goRoot + "/bin:" + e[5:]
			return env
		}
	}
	return append(env, "PATH="+goRoot+"/bin:/usr/bin:/bin")
}
