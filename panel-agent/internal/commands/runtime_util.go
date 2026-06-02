package commands

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Validation regexes for runtime inputs. The agent is the privileged
// executor: it renders these values into systemd unit files and docker
// command lines, so it MUST validate independently of the panel-api
// (defense in depth — never trust the caller).
var (
	// envKeyRe matches a POSIX-style environment variable name.
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// imageNameRe matches a docker image reference. Must start with an
	// alphanumeric (never '-', which would be parsed as a flag) and
	// contain only characters legal in a registry/name:tag@digest ref.
	imageNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)
	// safeTokenRe matches a hostname-like token (domain / runtime type):
	// no whitespace, quotes, or shell/systemd metacharacters.
	safeTokenRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// knownRuntimeTypes is the closed set the agent will render. Mirrors
// models.ValidRuntimeTypes on the panel-api side (separate Go module).
var knownRuntimeTypes = map[string]bool{
	"php": true, "nodejs": true, "python": true,
	"go": true, "docker": true, "static": true,
}

// validateRuntimeType rejects runtime types outside the known set so a
// crafted value can't be smuggled into the unit-file Description line.
func validateRuntimeType(rt string) error {
	if !knownRuntimeTypes[rt] {
		return fmt.Errorf("invalid runtime_type %q", rt)
	}
	return nil
}

// validateSafeToken guards values that flow verbatim into an ExecStart
// or unit Description (domain name, runtime type). Blocks spaces,
// quotes, newlines, and other metacharacters that could break out of
// the directive.
func validateSafeToken(name, val string) error {
	if val == "" || len(val) > 253 || !safeTokenRe.MatchString(val) {
		return fmt.Errorf("invalid %s %q", name, val)
	}
	return nil
}

// validateImageName rejects docker image references that could inject
// extra `docker run` flags or arguments. The image name is rendered as
// a bare token in the systemd ExecStart line, so a value like
// "alpine --privileged -v /:/host" would otherwise add flags that mount
// the host root or grant privileged mode — a container escape.
func validateImageName(img string) error {
	if img == "" || len(img) > 255 || !imageNameRe.MatchString(img) {
		return fmt.Errorf("invalid docker image reference %q", img)
	}
	return nil
}

// validateEntryPoint rejects path-traversal in node/python entry points.
// The entry is joined with the docroot and executed; ".." segments
// could point the interpreter at a file outside the tenant's docroot.
func validateEntryPoint(entry string) error {
	if entry == "" {
		return nil // runtime-specific default applies
	}
	if strings.ContainsAny(entry, "\x00\n\r") {
		return fmt.Errorf("entry_point contains control characters")
	}
	if filepath.IsAbs(entry) {
		return fmt.Errorf("entry_point must be relative to the docroot")
	}
	for _, seg := range strings.Split(filepath.ToSlash(entry), "/") {
		if seg == ".." {
			return fmt.Errorf("entry_point must not escape the docroot")
		}
	}
	return nil
}

// validateEnvVars rejects keys/values that could break out of the
// systemd `Environment="KEY=VALUE"` directive and inject new unit lines
// (e.g. an extra ExecStartPre= or a PATH override). Keys must be POSIX
// names; values may not contain newlines, NULs, double quotes, or
// backslashes (the chars that have meaning inside a quoted systemd
// directive).
func validateEnvVars(env map[string]string) error {
	for k, v := range env {
		if !envKeyRe.MatchString(k) {
			return fmt.Errorf("invalid environment variable name %q", k)
		}
		if strings.ContainsAny(v, "\x00\n\r\"\\") {
			return fmt.Errorf("environment value for %q contains a forbidden character (newline, quote, or backslash)", k)
		}
	}
	return nil
}

// resolveExecutable looks up the absolute path of standard runtime tools (node, npm, python, pip, go, docker)
// for the given user, falling back to global system paths if not found in home dirs.
func resolveExecutable(username, tool string) string {
	switch tool {
	case "nodejs":
		tool = "node"
		fallthrough
	case "node":
		// Look in user's home .nvm
		if matches, err := filepath.Glob(fmt.Sprintf("/home/%s/.nvm/versions/node/*/bin/node", username)); err == nil && len(matches) > 0 {
			return matches[len(matches)-1]
		}
		// Look in standard paths
		for _, path := range []string{"/usr/bin/node", "/usr/local/bin/node"} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "node"
	case "npm":
		// Look in user's home .nvm
		if matches, err := filepath.Glob(fmt.Sprintf("/home/%s/.nvm/versions/node/*/bin/npm", username)); err == nil && len(matches) > 0 {
			return matches[len(matches)-1]
		}
		for _, path := range []string{"/usr/bin/npm", "/usr/local/bin/npm"} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "npm"
	case "python":
		tool = "python3"
		fallthrough
	case "python3":
		// Prefer a user venv only when it looks complete (python3 + pip3).
		venvPython := fmt.Sprintf("/home/%s/.venv/bin/python3", username)
		venvPip := fmt.Sprintf("/home/%s/.venv/bin/pip3", username)
		if _, err := os.Stat(venvPython); err == nil {
			if _, pipErr := os.Stat(venvPip); pipErr == nil {
				return venvPython
			}
		}
		for _, path := range []string{
			fmt.Sprintf("/home/%s/.local/bin/python3", username),
			"/usr/bin/python3",
			"/usr/local/bin/python3",
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "python3"
	case "pip3":
		for _, path := range []string{
			fmt.Sprintf("/home/%s/.venv/bin/pip3", username),
			fmt.Sprintf("/home/%s/.local/bin/pip3", username),
			"/usr/bin/pip3",
			"/usr/local/bin/pip3",
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "pip3"
	case "go":
		for _, path := range []string{
			fmt.Sprintf("/home/%s/go-install/go/bin/go", username),
			"/usr/local/go/bin/go",
			"/usr/bin/go",
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "go"
	case "docker":
		for _, path := range []string{"/usr/bin/docker", "/usr/local/bin/docker"} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "docker"
	}
	return ""
}

// portInUseOnHost reports whether something is already listening on
// 127.0.0.1:port. The panel's PortAllocator only knows about
// panel-managed runtimes (it counts rows in runtime_services); a port
// could still be taken by an unrelated host process. The agent is the
// only component that can actually see the host's sockets, so it does
// the final check here before binding a managed runtime to the port.
//
// Best-effort: a failed dial means "likely free". We dial rather than
// listen so we don't briefly occupy the port ourselves.
func portInUseOnHost(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false // nothing accepted a connection => treat as free
	}
	_ = conn.Close()
	return true
}

// writeUserFile writes content to dir/name owned by the target user,
// using a temp file + chown + atomic rename (the same pattern used for
// the systemd unit). mode is applied to the final file.
func writeUserFile(ctx context.Context, username, dir, name string, content []byte, mode os.FileMode) error {
	finalPath := filepath.Join(dir, name)
	tmpFile := filepath.Join(dir, ".tmp-"+name)
	if err := os.WriteFile(tmpFile, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmpFile, mode); err != nil {
		os.Remove(tmpFile)
		return err
	}
	chownCmd := exec.CommandContext(ctx, "chown", username+":"+username, tmpFile)
	if err := chownCmd.Run(); err != nil {
		os.Remove(tmpFile)
		return err
	}
	if err := os.Rename(tmpFile, finalPath); err != nil {
		os.Remove(tmpFile)
		return err
	}
	return nil
}
