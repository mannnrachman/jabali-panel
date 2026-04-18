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
					"fi; "+
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
