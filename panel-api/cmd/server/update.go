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

	// lastBuiltSHAPath is where the SHA of the last fully-rebuilt
	// commit is persisted. Compared against HEAD on each update to
	// decide whether to run the build+restart chain. See runUpdate
	// for the self-heal rationale (why we don't compare pre==post).
	lastBuiltSHAPath = "/var/lib/jabali-panel/last-built-sha"
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
  2. If HEAD differs from /var/lib/jabali-panel/last-built-sha: npm ci,
     vite build, go build (panel-api + panel-agent), install new binaries,
     run pending migrations, restart services — then write HEAD back to
     last-built-sha. A partial-build failure leaves the file stale, so
     the next update retries automatically (self-heal).
  3. If HEAD matches last-built-sha: print "Already up to date" and exit.
     Use --force (-f) to run the rebuild + restart cycle anyway.`,
		// SilenceUsage so a runtime failure (apt 404, ENOTEMPTY race,
		// dirty migration) doesn't trigger cobra's full usage dump on
		// the operator's terminal — they want the error and a next
		// move, not the help text.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runUpdate(cmd, args)
			if err != nil {
				// Surface the repair hint directly under the failing
				// step. The error itself is printed by cobra after
				// RunE returns; the hint goes to stderr first so it's
				// adjacent to the error in the operator's scrollback.
				fmt.Fprint(os.Stderr, repairHint())
			}
			return err
		},
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

	// gitHead captures HEAD as a string. Runs via `sudo -u <serviceUser>`
	// because the repo is owned by the jabali user; git 2.35+ refuses to
	// operate on a repo owned by a different uid ("fatal: detected
	// dubious ownership"), which surfaces as exit 128.
	var postHEAD string
	gitHead := func() (string, error) {
		c := exec.Command("sudo", "-u", serviceUser,
			"git", "-C", repoDir, "rev-parse", "HEAD")
		out, err := c.Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	// readLastBuiltSHA returns the SHA written after the last fully-
	// successful rebuild, or "" if the file doesn't exist (fresh install,
	// or a previous update failed before it could write). An IO error
	// other than "not exists" returns the error — better to bail out
	// than silently rebuild a quiescent host.
	readLastBuiltSHA := func() (string, error) {
		b, err := os.ReadFile(lastBuiltSHAPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", fmt.Errorf("read %s: %w", lastBuiltSHAPath, err)
		}
		return strings.TrimSpace(string(b)), nil
	}

	// writeLastBuiltSHA persists the given SHA atomically (temp + rename)
	// so a crash mid-write can't leave a corrupt file. Called only after
	// the full build+restart chain succeeds.
	writeLastBuiltSHA := func(sha string) error {
		if err := os.MkdirAll("/var/lib/jabali-panel", 0o755); err != nil {
			return fmt.Errorf("mkdir /var/lib/jabali-panel: %w", err)
		}
		tmp := lastBuiltSHAPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(sha+"\n"), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, lastBuiltSHAPath); err != nil {
			return fmt.Errorf("rename %s → %s: %w", tmp, lastBuiltSHAPath, err)
		}
		return nil
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
			// Also chown panel-ui/test-results/ + playwright-report/
			// since `npx playwright test` run as root drops files
			// there which then block sudo-as-jabali `git reset --hard`
			// with "unable to unlink: Permission denied". Both dirs
			// are .gitignore'd; chown is purely to let `git reset`
			// remove any leftovers from a previous Playwright run.
			if err := run("", "chown", "-R",
				serviceUser+":"+serviceUser, repoDir+"/.git"); err != nil {
				return err
			}
			// Ignore failures on the panel-ui dirs — they may not
			// exist yet on a fresh install, and missing dirs are not
			// a deployment-blocking condition.
			_ = run("", "bash", "-c",
				"shopt -s nullglob; "+
					"chown -R "+serviceUser+":"+serviceUser+" "+
					repoDir+"/panel-ui/test-results "+
					repoDir+"/panel-ui/playwright-report 2>/dev/null; "+
					"true")
			return nil
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
			// Keep in sync with install_jabali_slices() and
			// install_sso_reaper_timer() in install.sh.
			return run("", "bash", "-c",
				"set -e; "+
					"install -d -m 0755 /usr/local/libexec/jabali; "+
					"install -m 0755 "+repoDir+"/install/systemd/fpm-pre-start /usr/local/libexec/jabali/fpm-pre-start; "+
					"install -m 0755 "+repoDir+"/install/systemd/fpm-exec /usr/local/libexec/jabali/fpm-exec; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali.slice /etc/systemd/system/jabali.slice; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali-user.slice /etc/systemd/system/jabali-user.slice; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali-fpm@.service /etc/systemd/system/jabali-fpm@.service; "+
					// M22 sso reaper (ADR-0040). Idempotent: install -m
					// is a no-op when source matches target. enable --now
					// is a no-op when the timer is already enabled+active.
					"install -m 0644 "+repoDir+"/install/systemd/jabali-sso-reaper.service /etc/systemd/system/jabali-sso-reaper.service; "+
					"install -m 0644 "+repoDir+"/install/systemd/jabali-sso-reaper.timer /etc/systemd/system/jabali-sso-reaper.timer; "+
					"systemctl daemon-reload; "+
					"systemctl enable --now jabali-sso-reaper.timer")
		}},
		{"sync OnFailure + restart drop-ins", func() error {
			// Re-render /etc/systemd/system/<unit>.service.d/10-jabali-restart.conf
			// for every critical service (nginx/mariadb/pdns/redis/crowdsec/
			// systemd-resolved + jabali daemons). Idempotent — the helper
			// hashes before/after and only daemon-reloads on diff.
			//
			// Without this on the update path, hosts installed before the
			// units list grew (e.g. before the jabali-* additions) never
			// pick up the OnFailure=jabali-notify@%n hook for those units,
			// so a service.down event only fires from the polling
			// service_down event source — which is up to a 60s window
			// behind the actual crash.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_restart_drop_ins"); err != nil {
				fmt.Printf("  (install_restart_drop_ins failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync apparmor profiles", func() error {
			// Re-render every jabali AppArmor profile from
			// install/apparmor/ + reload distro system profiles
			// (mariadb/redis/pdns). Without this, profile edits in the
			// repo never reach existing hosts — operator has to ssh in
			// and `apparmor_parser -r` by hand. Detected first-time
			// installs preserve complain mode; existing hosts keep
			// whatever mode the operator chose per profile.
			//
			// Idempotent. Failure is logged but doesn't block the
			// update; running daemons keep their currently-loaded
			// profile (or none if AA is disabled on this host).
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			// first_install=0 → preserve existing per-profile mode.
			if err := run("", "bash", "-c",
				"source "+installSh+" && cleanup_apparmor_legacy && apply_apparmor_profiles 0 && apply_apparmor_system_profiles 0"); err != nil {
				fmt.Printf("  (apparmor profile sync failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync jabali-agent + jabali-panel units", func() error {
			// Re-render the jabali-agent.service and jabali-panel.service
			// unit files from install.sh's writers so hardening/env
			// changes (PrivateTmp drops, SupplementaryGroups additions,
			// RestartSec tweaks) reach existing hosts. Without this,
			// `jabali update` only copies binaries — the running unit
			// keeps the install-time directives until the operator
			// manually re-runs install.sh.
			//
			// The cascade is delicate: jabali-panel.service Requires=
			// jabali-agent.service, so restarting the agent restarts
			// panel-api (us). To avoid suicide-during-update, we
			// daemon-reload here and schedule the restart via a transient
			// one-shot 5s in the future — by then the parent shell has
			// returned and the cascade is harmless.
			//
			// Detection of "did anything change?" is by sha256 over the
			// before/after file content; we only schedule the restart
			// when content actually drifted.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			script := `set -e
sha_before_a=$(sha256sum /etc/systemd/system/jabali-agent.service 2>/dev/null | awk '{print $1}' || echo "")
sha_before_p=$(sha256sum /etc/systemd/system/jabali-panel.service 2>/dev/null | awk '{print $1}' || echo "")
source ` + installSh + `
write_agent_systemd_unit
write_systemd_unit
systemctl daemon-reload
sha_after_a=$(sha256sum /etc/systemd/system/jabali-agent.service 2>/dev/null | awk '{print $1}' || echo "")
sha_after_p=$(sha256sum /etc/systemd/system/jabali-panel.service 2>/dev/null | awk '{print $1}' || echo "")
if [ "$sha_before_a" != "$sha_after_a" ] || [ "$sha_before_p" != "$sha_after_p" ]; then
  # Schedule a deferred restart so the cascade (panel-api Requires= agent)
  # doesn't kill us mid-update. 5s gives the parent shell ample time to
  # return.
  systemd-run --on-active=5s --unit=jabali-agent-deferred-restart /bin/systemctl restart jabali-agent.service >/dev/null 2>&1 || true
  echo "  (agent/panel unit content changed — restart scheduled in 5s)"
fi
`
			if err := run("", "bash", "-c", script); err != nil {
				fmt.Printf("  (unit sync failed: %v — continuing)\n", err)
			}
			return nil
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
					// Reload the unit that actually exists. Debian/Ubuntu
					// ship the daemon as ssh.service; RHEL/Rocky ship it
					// as sshd.service. systemctl reload <name> aborts on
					// unknown units, which broke jabali update on Debian
					// 13 — pick the right name first.
					"if systemctl list-unit-files ssh.service >/dev/null 2>&1; then "+
					"  systemctl reload ssh; "+
					"elif systemctl list-unit-files sshd.service >/dev/null 2>&1; then "+
					"  systemctl reload sshd; "+
					"else "+
					"  echo 'no ssh/sshd unit found, skipping reload' >&2; "+
					"fi; "+
					// jabali service user in www-data group — needed for
					// the reconciler's per-user FPM socket stat-check.
					// usermod is idempotent; 'groups | grep -w' avoids an
					// unnecessary write when already a member.
					"groups "+serviceUser+" | grep -qw www-data || usermod -aG www-data "+serviceUser+"; "+
					// M13 SSH sandbox: refresh wrapper + nspawn-enter +
					// sudoers + mode files on every update. Idempotent.
					"getent group jabali-ssh-sandbox >/dev/null || groupadd --system jabali-ssh-sandbox; "+
					"install -d -m 0755 -o root -g root /etc/jabali /etc/jabali/users /var/lib/jabali-nspawn /var/lib/jabali-nspawn/images; "+
					"install -m 0755 -o root -g root "+repoDir+"/install/ssh/jabali-ssh-shell /usr/local/bin/jabali-ssh-shell; "+
					"install -m 0755 -o root -g root "+repoDir+"/install/ssh/jabali-nspawn-enter /usr/local/bin/jabali-nspawn-enter; "+
					"visudo -cf "+repoDir+"/install/ssh/jabali-nspawn-sudoers >/dev/null && install -m 0440 -o root -g root "+repoDir+"/install/ssh/jabali-nspawn-sudoers /etc/sudoers.d/jabali-nspawn; "+
					"[ -f /etc/jabali/ssh-sandbox-mode ] || { echo bubblewrap > /etc/jabali/ssh-sandbox-mode; chmod 0644 /etc/jabali/ssh-sandbox-mode; }; "+
					"[ -f /etc/jabali/default-nspawn-image ] || { echo debian-12-v1 > /etc/jabali/default-nspawn-image; chmod 0644 /etc/jabali/default-nspawn-image; }")
		}},
		{"npm ci", func() error {
			// Wipe node_modules before npm ci. npm ci's docs promise it
			// does this itself, but in practice it dies with
			//   ENOTEMPTY: directory not empty, rmdir '.../node_modules/vite'
			// whenever a prior partial install or filesystem quirk leaves
			// a half-removed package tree behind.
			//
			// The dance below makes that resilient:
			//   1. mv node_modules → node_modules.stale.<pid> so the
			//      target dir is gone in one atomic syscall (rm -rf can
			//      take seconds on a heavy tree, leaving a window where
			//      npm ci sees a half-deleted target and races).
			//   2. background-rm the stale tree so the install isn't
			//      blocked on it.
			//   3. run npm ci. If it fails (the ENOTEMPTY rotate race
			//      inside npm itself, or the partial-install case where
			//      "added N packages" prints but .bin/ is empty), wipe
			//      and retry once — empirically the second attempt
			//      lands clean. Two failures in a row points at a real
			//      package-lock issue and we surface that.
			return asUser(repoDir+"/panel-ui", "bash", "-c", `set -e
trash="node_modules.stale.$$"
if [ -d node_modules ]; then
  mv node_modules "$trash"
  ( rm -rf "$trash" 2>/dev/null & )
fi
attempt() { npm ci --no-audit --no-fund; }
if ! attempt; then
  echo "[jabali] npm ci failed, wiping node_modules and retrying once..." >&2
  rm -rf node_modules
  sleep 2
  attempt
fi
test -x node_modules/.bin/tsc || {
  echo "[jabali] npm ci reported success but node_modules/.bin/tsc is missing — partial install" >&2
  exit 1
}
`)
		}},
		{"build frontend", func() error {
			// vite's emptyDir unlinks every file under dist/ before
			// writing the new bundle. If any prior build left root-owned
			// artifacts there (e.g. a legacy update ran as root, or an
			// operator ran `npm run build` from a root shell), the
			// jabali-owned build here hits EACCES on unlink and aborts.
			// chown the tree to the service user each run — cheap,
			// idempotent, and immune to however dist got into that state.
			distDir := repoDir + "/panel-ui/dist"
			if _, err := os.Stat(distDir); err == nil {
				if err := run("", "chown", "-R",
					serviceUser+":"+serviceUser, distDir); err != nil {
					return err
				}
			}
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
		{"verify NTP time sync", func() error {
			// TOTP enrolment quietly breaks when the wall clock drifts
			// > 30s from real time (every code the authenticator app
			// generates falls outside the validation window). Re-run
			// install_time_sync on every update so a host whose NTP
			// daemon got disabled or a fresh-image clone whose clock
			// skewed re-converges automatically. Non-fatal — install
			// just warns if not synchronised yet.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_time_sync"); err != nil {
				fmt.Printf("  (install_time_sync failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync nginx panel vhost + websocket map", func() error {
			// Re-render /etc/nginx/sites-available/jabali-panel.conf
			// from the template, and (re-)install the WebSocket upgrade
			// map snippet at /etc/nginx/conf.d/jabali-websocket-map.conf.
			// Required for log streaming WS endpoints to work — without
			// the map, $connection_upgrade is empty and nginx -t fails;
			// without $http_host on Host header, backend builds WS URLs
			// without the :8443 port.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_nginx_websocket_map && install_nginx_panel_vhost"); err != nil {
				fmt.Printf("  (install_nginx_panel_vhost failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync bulwark systemd + env", func() error {
			// Re-install the jabali-webmail.service unit file, the
			// server-unix.js wrapper, the nginx upstream snippet, and
			// re-render /etc/jabali-panel/bulwark.env from repo so
			// changes (e.g. Restart=always, NODE_TLS_REJECT_UNAUTHORIZED)
			// reach existing hosts without a full install.sh run.
			// _install_bulwark_systemd is idempotent: install -m no-ops
			// when content matches; daemon-reload runs unconditionally;
			// _install_bulwark_env (called at the tail) restarts webmail
			// only when the env content actually changed. Tarball stays
			// untouched. Failure is non-fatal — old unit keeps working.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && _install_bulwark_systemd"); err != nil {
				fmt.Printf("  (_install_bulwark_systemd failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync stalwart spam-filter rules", func() error {
			// Vendor the pinned spam-filter rules bundle + (re-)install
			// the weekly auto-refresh timer + script. Existing hosts
			// without these (installed before feat/stalwart-spam-filter-pin)
			// still hit github.com/stalwartlabs/spam-filter at every
			// Stalwart cold start; this convergence step pins them to
			// /opt/stalwart/share/spam-filter-rules.json.gz once and
			// arms the timer to keep it refreshed.
			//
			// Apply-plan re-render + stalwart-cli re-apply is intentionally
			// skipped here — apply is create-only for some objects and the
			// SpamSettings update lives in the install-time plan path.
			// Operators on a stale apply-plan can re-run install.sh to
			// converge that. Non-fatal here either way.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && _install_spam_rules"); err != nil {
				fmt.Printf("  (_install_spam_rules failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync kratos config", func() error {
			// install_kratos is idempotent: if the binary is already at
			// the target version it skips the download but still
			// re-renders /etc/jabali-panel/kratos.yml from the template,
			// detects content drift via cmp, and restarts
			// jabali-kratos.service when the rendered config changed.
			//
			// Without this step, kratos.yml.tmpl edits (e.g. the
			// selfservice.methods.code.enabled flip in 8c93811 that the
			// v4 debug report flagged) reach the on-disk config but
			// never the running process — operator has to ssh in and
			// `systemctl restart jabali-kratos` by hand.
			//
			// Failure is logged but doesn't block the update; the
			// running Kratos keeps serving the old config.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_kratos"); err != nil {
				fmt.Printf("  (install_kratos failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync malware stack", func() error {
			// Source install.sh and re-run install_malware_stack so existing
			// hosts converge on amendments to the M33 stack (ADR-0072) without
			// manual SSH. Runs unconditionally — the function is fully
			// idempotent (LMD install gated by version marker; clamav apt
			// install gated by `command -v clamscan`; PMF tarball gated by
			// presence of php.yar; systemd unit writes are install -m no-ops
			// when content matches).
			//
			// install.sh has a BASH_SOURCE guard at its tail so sourcing it
			// does NOT trigger main()'s full install — only the named
			// function runs.
			//
			// Failure here does not block the update — malware stack is a
			// post-deploy convergence step, not a service the panel depends
			// on. Log and move on.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_malware_stack"); err != nil {
				fmt.Printf("  (install_malware_stack failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync PHP Defense (Snuffleupagus)", func() error {
			// M41 (ADR-0088). Re-runs install_snuffleupagus so the per-PHP
			// minor sp.so + conf.d wiring + rule bundle mirror converge on
			// `jabali update`. Idempotent — build.sh skips minors already
			// at the pinned tag (.jabali-version stamp); apt install of
			// phpX.Y-dev / build-essential / libpcre2-dev short-circuits
			// when already present.
			//
			// Without this step, fresh installs that were missing phpX.Y-dev
			// at install_snuffleupagus time stayed permanently broken until
			// manual intervention — rules + DB migrations landed but sp.so
			// never built. install.sh's main() runs install_snuffleupagus
			// once at install time only; update flow needs its own hook.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_snuffleupagus"); err != nil {
				fmt.Printf("  (install_snuffleupagus failed: %v — continuing)\n", err)
			}
			return nil
		}},
		{"sync adminer", func() error {
			// M37 Phase 4 — Adminer SSO bridge. install_adminer is
			// idempotent: re-runs are no-ops (file existence check
			// + install -m), so it's safe to call on every update.
			// Failure is logged but doesn't block the update.
			installSh := repoDir + "/install.sh"
			if _, err := os.Stat(installSh); err != nil {
				return nil
			}
			if err := run("", "bash", "-c",
				"source "+installSh+" && install_adminer"); err != nil {
				fmt.Printf("  (install_adminer failed: %v — continuing)\n", err)
			}
			return nil
		}},
	}

	for _, s := range prelude {
		fmt.Printf("→ %s\n", s.name)
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}

	// Fast path: we already fully rebuilt this SHA. The build + restart
	// cycle would do ~30-60 s of CPU work + bounce services, all for a
	// no-op. Skip unless the operator asked for a forced rebuild.
	//
	// Self-heal: if a PREVIOUS update advanced HEAD but failed before
	// last-built-sha was written, lastBuilt stays at the old SHA (or ""
	// on a fresh host) and we re-run the build chain — which is what
	// the operator wanted. The earlier implementation compared
	// preHEAD==postHEAD, which would have skipped the rebuild in that
	// stuck state, requiring --force to recover.
	lastBuilt, err := readLastBuiltSHA()
	if err != nil {
		return err
	}
	if lastBuilt == postHEAD && !force {
		shortSHA := postHEAD
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

	// Record the SHA we just fully built + restarted against. Must be
	// the LAST thing we do — if any step above fails, we DON'T write,
	// and the next update retries the build chain automatically.
	if err := writeLastBuiltSHA(postHEAD); err != nil {
		// Don't fail the whole update for a cosmetic bookkeeping miss;
		// binaries + migrations + services are already updated. Next
		// run will simply rebuild once more (harmless).
		fmt.Printf("  (warning: could not persist last-built-sha: %v)\n", err)
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
