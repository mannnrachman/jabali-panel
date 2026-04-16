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
}

// ServiceListResponse is the payload for service.list.
type ServiceListResponse struct {
	Services []ServiceStatus `json:"services"`
}

// AllowedServices is the set of services the agent will report on. This is
// a security boundary: callers can't probe arbitrary systemd units.
// Production code may override this via config; tests override directly.
var AllowedServices = []string{
	"nginx",
	"mariadb",
	"redis-server",
	"php8.1-fpm",
	"php8.2-fpm",
	"php8.3-fpm",
	"php8.4-fpm",
	"stalwart-mail",
	"named",
	"jabali-panel",
	"jabali-agent",
}

// systemctlRunner abstracts exec.Command for testing.
var systemctlRunner = realSystemctl

func realSystemctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func serviceListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	services := make([]ServiceStatus, 0, len(AllowedServices))
	for _, svc := range AllowedServices {
		status := probeService(ctx, svc)
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

	out, err := systemctlRunner(ctx, "is-active", fmt.Sprintf("%s.service", name))
	if err != nil {
		// systemctl exits non-zero for inactive/failed; the output still
		// contains the state word.
		if out == "" {
			return ServiceStatus{Name: name, Active: "unknown"}
		}
	}
	state := strings.TrimSpace(out)
	switch state {
	case "active", "inactive", "failed", "activating", "deactivating":
		return ServiceStatus{Name: name, Active: state}
	default:
		return ServiceStatus{Name: name, Active: "unknown"}
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
