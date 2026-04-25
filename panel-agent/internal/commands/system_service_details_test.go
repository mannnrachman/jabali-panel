package commands

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const systemctlShowSample = `ActiveState=active
SubState=running
LoadState=loaded
MemoryCurrent=12345678
TasksCurrent=12
ActiveEnterTimestamp=Fri 2026-04-25 10:00:00 UTC
Id=jabali-panel.service

ActiveState=inactive
SubState=dead
LoadState=loaded
MemoryCurrent=[not set]
TasksCurrent=[not set]
ActiveEnterTimestamp=
Id=mariadb.service`

func TestParseSystemctlShow_HappyPath(t *testing.T) {
	now := time.Date(2026, 4, 25, 11, 0, 0, 0, time.UTC) // 1h after panel start
	details := parseSystemctlShow(systemctlShowSample, now)
	require.Len(t, details, 2)

	panel := details[0]
	assert.Equal(t, "jabali-panel.service", panel.Unit)
	assert.Equal(t, "active", panel.Active)
	assert.Equal(t, "running", panel.Sub)
	assert.Equal(t, uint64(12345678), panel.MemoryBytes)
	assert.Equal(t, 12, panel.Tasks)
	assert.InDelta(t, 3600, panel.UptimeSeconds, 1.0,
		"uptime must reflect now - ActiveEnterTimestamp")

	mariadb := details[1]
	assert.Equal(t, "mariadb.service", mariadb.Unit)
	assert.Equal(t, "inactive", mariadb.Active)
	assert.Equal(t, uint64(0), mariadb.MemoryBytes,
		"[not set] must coerce to 0")
	assert.Equal(t, int64(0), mariadb.UptimeSeconds,
		"empty timestamp must yield zero uptime")
}

func TestParseSystemdUint(t *testing.T) {
	assert.Equal(t, uint64(0), parseSystemdUint(""))
	assert.Equal(t, uint64(0), parseSystemdUint("[not set]"))
	assert.Equal(t, uint64(0), parseSystemdUint("garbage"))
	assert.Equal(t, uint64(42), parseSystemdUint("42"))
}

func TestSystemServiceDetailsHandler_AllowlistFilters(t *testing.T) {
	// Capture args + return canned output. systemctl args will list
	// only the allowlisted unit even though the caller asked for both.
	var capturedArgs []string
	orig := systemctlRunner
	t.Cleanup(func() { systemctlRunner = orig })
	systemctlRunner = func(_ context.Context, args ...string) (string, error) {
		capturedArgs = append([]string{}, args...)
		return `ActiveState=active
SubState=running
LoadState=loaded
MemoryCurrent=100
TasksCurrent=1
ActiveEnterTimestamp=
Id=jabali-panel.service`, nil
	}

	req := json.RawMessage(`{"units":["jabali-panel.service","random.service"]}`)
	resp, err := systemServiceDetailsHandler(context.Background(), req)
	require.NoError(t, err)
	out := resp.(SystemServiceDetailsResponse)
	require.Len(t, out.Services, 1)
	assert.Equal(t, "jabali-panel.service", out.Services[0].Unit)

	// Verify random.service was stripped before handing to systemctl —
	// the captured args must include the allowlisted unit and NOT the
	// random one.
	hasPanel := false
	hasRandom := false
	for _, a := range capturedArgs {
		if a == "jabali-panel.service" {
			hasPanel = true
		}
		if a == "random.service" {
			hasRandom = true
		}
	}
	assert.True(t, hasPanel, "panel unit must reach systemctl")
	assert.False(t, hasRandom, "non-allowlisted unit must be filtered before exec")
}

func TestSystemServiceDetailsHandler_DefaultsToAllowlist(t *testing.T) {
	orig := systemctlRunner
	t.Cleanup(func() { systemctlRunner = orig })
	called := false
	systemctlRunner = func(_ context.Context, args ...string) (string, error) {
		called = true
		// Verify at least nginx + mariadb made it into args.
		hasNginx, hasMariadb := false, false
		for _, a := range args {
			if a == "nginx.service" {
				hasNginx = true
			}
			if a == "mariadb.service" {
				hasMariadb = true
			}
		}
		assert.True(t, hasNginx)
		assert.True(t, hasMariadb)
		return "", nil
	}
	_, err := systemServiceDetailsHandler(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, called)
}
