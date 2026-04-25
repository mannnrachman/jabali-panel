package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// systemAptCheckResponse is the wire shape for system.apt_check.
type systemAptCheckResponse struct {
	Packages []aptUpgradablePackage `json:"packages"`
	Total    int                    `json:"total"`
}

type aptUpgradablePackage struct {
	Name    string `json:"name"`
	Current string `json:"current"`
	New     string `json:"new"`
	Source  string `json:"source"`
}

// systemAptCheckHandler runs `apt-get update` then `apt list --upgradable`
// and parses the column-stable output. Every apt invocation includes
// `-o DPkg::Lock::Timeout=60` because unattended-upgrades.timer runs
// nightly and holds the dpkg lock; without the timeout, ops see a
// cryptic crash. LC_ALL=C pins the column header locale.
//
// This command takes NO user-controlled parameters — apt is invoked with
// fixed args only. Any future params should be type-tagged enums, not
// passed through as strings.
func systemAptCheckHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	if err := runApt(ctx, "update", "-qq"); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("apt-get update: %v", err)}
	}
	out, err := aptList(ctx)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("apt list: %v", err)}
	}
	pkgs := parseAptUpgradable(string(out))
	return systemAptCheckResponse{Packages: pkgs, Total: len(pkgs)}, nil
}

func runApt(ctx context.Context, args ...string) error {
	full := append([]string{"-o", "DPkg::Lock::Timeout=60"}, args...)
	cmd := exec.CommandContext(ctx, "apt-get", full...)
	cmd.Env = append(cmd.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func aptList(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "apt", "list", "--upgradable")
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseAptUpgradable reads `apt list --upgradable` output, format example:
//
//	Listing...
//	curl/stable 8.5.0-2 amd64 [upgradable from: 8.4.0-2]
//	libc6/stable 2.36-9+deb12u4 amd64 [upgradable from: 2.36-9+deb12u3]
var aptListLine = regexp.MustCompile(`^([A-Za-z0-9.+\-]+)/(\S+)\s+(\S+)\s+(\S+)\s+\[upgradable from:\s*(\S+?)\]$`)

func parseAptUpgradable(out string) []aptUpgradablePackage {
	var pkgs []aptUpgradablePackage
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Listing") || strings.HasPrefix(line, "WARNING:") {
			continue
		}
		m := aptListLine.FindStringSubmatch(line)
		if len(m) != 6 {
			continue
		}
		pkgs = append(pkgs, aptUpgradablePackage{
			Name:    m[1],
			Source:  m[2],
			New:     m[3],
			Current: m[5],
		})
	}
	return pkgs
}

func init() {
	Default.Register("system.apt_check", systemAptCheckHandler)
}
