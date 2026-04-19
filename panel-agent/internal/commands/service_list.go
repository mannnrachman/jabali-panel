package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ServiceStatus describes a single systemd service's state.
type ServiceStatus struct {
	Name   string `json:"name"`
	Active string `json:"active"` // "active", "inactive", "failed", "unknown"
	// LoadState surfaces whether the unit can be controlled:
	//   "loaded" — normal; restartable.
	//   "masked" — unit is deliberately blocked; restart would fail.
	//   "not-found" — unit doesn't exist (filtered out upstream).
	//   "error" — systemd couldn't parse the unit file.
	LoadState string `json:"load_state"`
}

// ServiceListResponse is the payload for service.list.
type ServiceListResponse struct {
	Services []ServiceStatus `json:"services"`
}

// BaseAllowedServices is the fixed set of services the agent will report
// on — the PHP-FPM list is appended dynamically from SupportedPHPVersions
// so adding a new PHP version to that constant also adds it to the
// dashboard. This is a security boundary: callers can't probe arbitrary
// systemd units.
var BaseAllowedServices = []string{
	"nginx",
	"mariadb",
	"redis-server",
	"stalwart-mail",
	"pdns", // PowerDNS, not BIND — see ADR-0003
	"jabali-panel",
	"jabali-agent",
}

// AllowedServices returns the full list (base + one php<v>-fpm per
// supported version). Kept as a function so SupportedPHPVersions edits
// flow through without a restart-to-regenerate cycle.
func AllowedServices() []string {
	out := make([]string, 0, len(BaseAllowedServices)+len(SupportedPHPVersions))
	out = append(out, BaseAllowedServices...)
	for _, v := range SupportedPHPVersions {
		out = append(out, fmt.Sprintf("php%s-fpm", v))
	}
	return out
}

// systemctlRunner abstracts exec.Command for testing.
var systemctlRunner = realSystemctl

func realSystemctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func serviceListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	names := AllowedServices()
	services := make([]ServiceStatus, 0, len(names))
	for _, svc := range names {
		status := probeService(ctx, svc)
		// Skip services that aren't installed on this host — the
		// dashboard should reflect reality, not a wishlist. LoadState
		// "not-found" means systemd can't find the unit file.
		if status.LoadState == "not-found" || status.LoadState == "" {
			continue
		}
		services = append(services, status)
	}
	return ServiceListResponse{Services: services}, nil
}

func probeService(ctx context.Context, name string) ServiceStatus {
	// Validate service name to prevent injection. Only allow
	// alphanumeric, hyphen, dot, and @ (for template units).
	for _, c := range name {
		if !isServiceNameChar(c) {
			return ServiceStatus{Name: name, Active: "unknown"}
		}
	}

	unit := fmt.Sprintf("%s.service", name)

	// LoadState tells us whether the unit file exists on disk
	// ("loaded" | "masked" | "not-found" | "error"). We use it both
	// to filter out uninstalled services and to let the UI decide
	// whether a restart button makes sense (masked => no).
	loadState, _ := systemctlRunner(ctx, "show", "-p", "LoadState", "--value", unit)
	loadState = strings.TrimSpace(loadState)

	out, err := systemctlRunner(ctx, "is-active", unit)
	if err != nil {
		// systemctl exits non-zero for inactive/failed; the output still
		// contains the state word.
		if out == "" {
			return ServiceStatus{Name: name, Active: "unknown", LoadState: loadState}
		}
	}
	state := strings.TrimSpace(out)
	switch state {
	case "active", "inactive", "failed", "activating", "deactivating":
		return ServiceStatus{Name: name, Active: state, LoadState: loadState}
	default:
		return ServiceStatus{Name: name, Active: "unknown", LoadState: loadState}
	}
}

func isServiceNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '.' || c == '@' || c == '_'
}

func init() {
	Default.Register("service.list", serviceListHandler)
}
