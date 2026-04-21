package commands

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installFakeSystemctl swaps in a deterministic systemctl responder for
// the duration of a test. State is a per-unit map keyed by the bare
// service name (no ".service" suffix).
type fakeServiceState struct {
	active    string // "active" | "inactive" | "failed"
	loadState string // "loaded" | "masked" | "not-found"
}

func installFakeSystemctl(t *testing.T, state map[string]fakeServiceState) {
	t.Helper()
	orig := systemctlRunner
	t.Cleanup(func() { systemctlRunner = orig })

	systemctlRunner = func(_ context.Context, args ...string) (string, error) {
		if len(args) == 0 {
			return "", errors.New("no args")
		}
		// systemctl show -p LoadState --value <name>.service
		if args[0] == "show" && len(args) >= 5 {
			unit := args[4]
			name := unit[:len(unit)-len(".service")]
			s, ok := state[name]
			if !ok || s.loadState == "" {
				return "not-found", nil
			}
			return s.loadState, nil
		}
		// systemctl is-active <name>.service
		if args[0] == "is-active" && len(args) >= 2 {
			unit := args[1]
			name := unit[:len(unit)-len(".service")]
			s, ok := state[name]
			if !ok {
				return "inactive", errors.New("exit status 3")
			}
			if s.active == "active" {
				return "active", nil
			}
			return s.active, errors.New("exit status 3")
		}
		// systemctl restart <name>.service
		if args[0] == "restart" && len(args) >= 2 {
			unit := args[1]
			name := unit[:len(unit)-len(".service")]
			s := state[name]
			s.active = "active"
			state[name] = s
			return "", nil
		}
		return "", errors.New("unexpected args")
	}
}

// TestServiceList_FiltersNotInstalled — dashboard should hide services
// that aren't on the host (LoadState=not-found) AND services that are
// deliberately masked (per ADR-0025 the global php<v>-fpm.service units
// are masked on every host; showing them on the dashboard with a greyed
// Restart button is noise).
func TestServiceList_FiltersNotInstalled(t *testing.T) {
	origBase := BaseAllowedServices
	t.Cleanup(func() {
		BaseAllowedServices = origBase
	})
	BaseAllowedServices = []string{"nginx", "mariadb", "pdns", "masked-service"}

	installFakeSystemctl(t, map[string]fakeServiceState{
		"nginx":          {active: "active", loadState: "loaded"},
		"mariadb":        {active: "active", loadState: "loaded"},
		"masked-service": {active: "inactive", loadState: "masked"},
		// pdns has no state → LoadState=not-found → filtered.
	})

	r := NewRegistry()
	r.Register("service.list", serviceListHandler)
	data, agentErr := r.Dispatch(context.Background(), "service.list", nil)
	require.Nil(t, agentErr)

	var resp ServiceListResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	require.Len(t, resp.Services, 2, "only loaded+installed services should be returned; masked and not-found are filtered")

	names := make(map[string]ServiceStatus)
	for _, s := range resp.Services {
		names[s.Name] = s
	}
	assert.Contains(t, names, "nginx")
	assert.Contains(t, names, "mariadb")
	assert.NotContains(t, names, "pdns")
	assert.NotContains(t, names, "masked-service")
}

// TestServiceList_UsesPdnsNotNamed — PowerDNS is the nameserver of
// record (ADR-0003 / M6), the legacy `named` entry was wrong.
func TestServiceList_UsesPdnsNotNamed(t *testing.T) {
	assert.Contains(t, BaseAllowedServices, "pdns")
	assert.NotContains(t, BaseAllowedServices, "named")
}

// TestServiceList_NoGlobalPHPFPM — global php<v>-fpm units are masked
// on every host by install.sh (per ADR-0025; per-user FPMs run under
// jabali-fpm@<user>.service). They MUST NOT appear in the allow-list
// or on the dashboard — showing masked services produces user-visible
// noise (greyed-out Restart for a service that's architecturally dead).
func TestServiceList_NoGlobalPHPFPM(t *testing.T) {
	all := AllowedServices()
	for _, v := range SupportedPHPVersions {
		assert.NotContains(t, all, "php"+v+"-fpm",
			"global php%s-fpm must not be in allow-list — masked per ADR-0025", v)
	}
}

func TestIsServiceNameChar(t *testing.T) {
	t.Parallel()

	for _, c := range "abcABC019-._@" {
		assert.True(t, isServiceNameChar(c), "should allow %c", c)
	}
	for _, c := range ";&|$`'\"()/\\ " {
		assert.False(t, isServiceNameChar(c), "should reject %c", c)
	}
}
