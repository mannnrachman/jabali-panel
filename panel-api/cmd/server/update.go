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
	return &cobra.Command{
		Use:   "update",
		Short: "Pull latest code, rebuild, migrate, and restart services",
		Long: `Performs a full self-update:
  1. git pull in the repo directory
  2. npm ci + npm run build (panel-ui)
  3. go build (panel-api + panel-agent)
  4. Install new binaries
  5. Run pending migrations
  6. Restart jabali-panel + jabali-agent services`,
		RunE: runUpdate,
	}
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

	steps := []struct {
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
		{"git pull", func() error {
			return asUser(repoDir, "git", "pull", "--ff-only", "origin", "main")
		}},
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
			return asUser(repoDir+"/panel-ui", "npm", "ci", "--no-audit", "--no-fund")
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

	for _, s := range steps {
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
