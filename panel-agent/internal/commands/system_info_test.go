package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUptimeData(t *testing.T) {
	t.Parallel()

	t.Run("normal", func(t *testing.T) {
		up, err := ParseUptimeData("12345.67 98765.43\n")
		require.NoError(t, err)
		assert.InDelta(t, 12345.67, up, 0.01)
	})

	t.Run("empty", func(t *testing.T) {
		_, err := ParseUptimeData("")
		assert.Error(t, err)
	})
}

func TestParseLoadAvgData(t *testing.T) {
	t.Parallel()

	t.Run("normal", func(t *testing.T) {
		avg, err := ParseLoadAvgData("0.15 0.10 0.05 1/234 5678\n")
		require.NoError(t, err)
		assert.InDelta(t, 0.15, avg[0], 0.001)
		assert.InDelta(t, 0.10, avg[1], 0.001)
		assert.InDelta(t, 0.05, avg[2], 0.001)
	})

	t.Run("too few fields", func(t *testing.T) {
		_, err := ParseLoadAvgData("0.15 0.10\n")
		assert.Error(t, err)
	})
}

func TestParseCPUCountData(t *testing.T) {
	t.Parallel()

	content := `processor	: 0
vendor_id	: GenuineIntel
model name	: Intel(R) Core(TM) i7-9750H CPU

processor	: 1
vendor_id	: GenuineIntel
model name	: Intel(R) Core(TM) i7-9750H CPU

processor	: 2
vendor_id	: GenuineIntel
`
	assert.Equal(t, 3, ParseCPUCountData(content))

	t.Run("empty", func(t *testing.T) {
		assert.Equal(t, 1, ParseCPUCountData(""))
	})
}

func TestParseMeminfoData(t *testing.T) {
	t.Parallel()

	content := `MemTotal:       16384000 kB
MemFree:         2048000 kB
MemAvailable:    8192000 kB
Buffers:          512000 kB
Cached:          4096000 kB
`

	t.Run("normal", func(t *testing.T) {
		total, avail, err := ParseMeminfoData(content)
		require.NoError(t, err)
		assert.Equal(t, uint64(16384000), total)
		assert.Equal(t, uint64(8192000), avail)
	})

	t.Run("missing MemTotal", func(t *testing.T) {
		_, _, err := ParseMeminfoData("MemAvailable: 100 kB\n")
		assert.Error(t, err)
	})

	t.Run("missing MemAvailable", func(t *testing.T) {
		_, _, err := ParseMeminfoData("MemTotal: 100 kB\n")
		assert.Error(t, err)
	})
}

func TestSystemInfoHandler_Dispatch(t *testing.T) {
	t.Parallel()

	// Use the Default registry which has system.info registered via init().
	r := NewRegistry()
	r.Register("system.info", systemInfoHandler)

	data, agentErr := r.Dispatch(context.Background(), "system.info", nil)
	// On a real Linux box this should succeed; on CI it depends on /proc.
	if agentErr != nil {
		t.Skipf("system.info not available (probably no /proc): %s", agentErr.Message)
	}

	var resp SystemInfoResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	assert.NotEmpty(t, resp.Hostname)
	assert.Greater(t, resp.UptimeSeconds, float64(0))
	assert.Greater(t, resp.MemTotalKB, uint64(0))
	assert.GreaterOrEqual(t, resp.MemAvailKB, uint64(0))
	assert.Greater(t, resp.CPUCount, 0)
	assert.NotEmpty(t, resp.Partitions)
}
