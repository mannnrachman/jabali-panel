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
// that aren't on the host (LoadState=not-found). Before this filter,
// a stock Debian box showed php8.1/8.2/8.3 rows for versions the admin
// never installed.
func TestServiceList_FiltersNotInstalled(t *testing.T) {
	origBase := BaseAllowedServices
	origSupported := SupportedPHPVersions
	t.Cleanup(func() {
		BaseAllowedServices = origBase
		SupportedPHPVersions = origSupported
	})
	BaseAllowedServices = []string{"nginx", "mariadb", "pdns"}
	SupportedPHPVersions = []string{"8.4", "8.5"}

	installFakeSystemctl(t, map[string]fakeServiceState{
		"nginx":      {active: "active", loadState: "loaded"},
		"mariadb":    {active: "active", loadState: "loaded"},
		"php8.5-fpm": {active: "inactive", loadState: "masked"},
		// pdns and php8.4-fpm have no state → LoadState=not-found → filtered.
	})

	r := NewRegistry()
	r.Register("service.list", serviceListHandler)
	data, agentErr := r.Dispatch(context.Background(), "service.list", nil)
	require.Nil(t, agentErr)

	var resp ServiceListResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	require.Len(t, resp.Services, 3, "only installed services should be returned")

	names := make(map[string]ServiceStatus)
	for _, s := range resp.Services {
		names[s.Name] = s
	}
	assert.Contains(t, names, "nginx")
	assert.Contains(t, names, "mariadb")
	assert.Contains(t, names, "php8.5-fpm")
	assert.NotContains(t, names, "pdns")
	assert.NotContains(t, names, "php8.4-fpm")

	// 8.5 is masked (per ADR-0025); UI needs LoadState to hide the
	// Restart button for it.
	assert.Equal(t, "masked", names["php8.5-fpm"].LoadState)
	assert.Equal(t, "inactive", names["php8.5-fpm"].Active)
}

// TestServiceList_UsesPdnsNotNamed — PowerDNS is the nameserver of
// record (ADR-0003 / M6), the legacy `named` entry was wrong.
func TestServiceList_UsesPdnsNotNamed(t *testing.T) {
	assert.Contains(t, BaseAllowedServices, "pdns")
	assert.NotContains(t, BaseAllowedServices, "named")
}

// TestServiceList_PHPVersionsDynamic — adding a PHP version to the
// supported list makes it appear on the dashboard (when installed).
func TestServiceList_PHPVersionsDynamic(t *testing.T) {
	all := AllowedServices()
	for _, v := range SupportedPHPVersions {
		assert.Contains(t, all, "php"+v+"-fpm")
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
