package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const procNetDevSample = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 12345      99    0    0    0     0          0         0    12345      99    0    0    0     0       0          0
  eth0: 1000000  1000    0    0    0     0          0         0   500000     500    0    0    0     0       0          0
`

func TestParseProcNetDev(t *testing.T) {
	rows := parseProcNetDev(procNetDevSample)
	require.Len(t, rows, 2)
	assert.Equal(t, "lo", rows[0].iface)
	assert.Equal(t, uint64(12345), rows[0].rxBytes)
	assert.Equal(t, "eth0", rows[1].iface)
	assert.Equal(t, uint64(1000000), rows[1].rxBytes)
	assert.Equal(t, uint64(1000), rows[1].rxPackets)
	assert.Equal(t, uint64(500000), rows[1].txBytes)
}

func TestRateDelta_HandlesCounterReset(t *testing.T) {
	// curr < prev = NIC/kernel reset → rate must be 0, never an underflow.
	assert.Equal(t, uint64(0), rateDelta(50, 100, 1.0))
	assert.Equal(t, uint64(100), rateDelta(200, 100, 1.0))
	assert.Equal(t, uint64(50), rateDelta(200, 100, 2.0))
}

func TestRateDelta_ZeroSecsSafe(t *testing.T) {
	// Caller is supposed to skip when secs=0; if it doesn't we still
	// don't want to panic on division.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("rateDelta panicked on zero secs: %v", r)
		}
	}()
	// Division-by-zero on float64 yields +Inf, cast to uint64 wraps.
	// We don't assert a specific value — just that it doesn't crash.
	_ = rateDelta(200, 100, 0.0)
}

func TestSystemNetworkHandler_FirstCallIsWarmingUp(t *testing.T) {
	// Reset cache to simulate a freshly-booted agent.
	netDevCacheMu.Lock()
	netDevCache = map[string]netDevSample{}
	netDevCacheMu.Unlock()

	dir := t.TempDir()
	path := filepath.Join(dir, "net-dev")
	require.NoError(t, os.WriteFile(path, []byte(procNetDevSample), 0o644))

	t.Cleanup(func(orig string) func() {
		return func() { netDevPath = orig }
	}(netDevPath))
	netDevPath = path

	resp, err := systemNetworkHandler(context.Background(), nil)
	require.NoError(t, err)
	out := resp.(SystemNetworkResponse)

	// lo is excluded by default.
	for _, iface := range out.Interfaces {
		assert.NotEqual(t, "lo", iface.Iface)
	}
	// The first-call interface must report warming_up.
	require.NotEmpty(t, out.Interfaces)
	assert.True(t, out.Interfaces[0].WarmingUp,
		"first call should mark interfaces warming_up")
	assert.Equal(t, uint64(0), out.Interfaces[0].RXBps)
}

func TestSystemNetworkHandler_SecondCallComputesRates(t *testing.T) {
	netDevCacheMu.Lock()
	netDevCache = map[string]netDevSample{}
	netDevCacheMu.Unlock()

	const sample1 = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: 1000     10     0    0    0     0          0         0    500      5       0    0    0     0       0          0
`
	const sample2 = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: 11000   110     0    0    0     0          0         0    1500     15      0    0    0     0       0          0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "net-dev")
	t.Cleanup(func(orig string) func() {
		return func() { netDevPath = orig }
	}(netDevPath))
	netDevPath = path

	t.Cleanup(func(orig func() time.Time) func() {
		return func() { netDevNowFn = orig }
	}(netDevNowFn))

	t0 := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	netDevNowFn = func() time.Time { return t0 }
	require.NoError(t, os.WriteFile(path, []byte(sample1), 0o644))
	_, err := systemNetworkHandler(context.Background(), nil)
	require.NoError(t, err)

	// 10s later: 10000 bytes RX = 1000 B/s, 1000 bytes TX = 100 B/s.
	netDevNowFn = func() time.Time { return t0.Add(10 * time.Second) }
	require.NoError(t, os.WriteFile(path, []byte(sample2), 0o644))
	resp, err := systemNetworkHandler(context.Background(), json.RawMessage(`{"include_loopback":false}`))
	require.NoError(t, err)
	out := resp.(SystemNetworkResponse)
	require.NotEmpty(t, out.Interfaces)
	eth0 := out.Interfaces[0]
	assert.Equal(t, "eth0", eth0.Iface)
	assert.False(t, eth0.WarmingUp, "second sample must not be warming up")
	assert.Equal(t, uint64(1000), eth0.RXBps)
	assert.Equal(t, uint64(100), eth0.TXBps)
	assert.Equal(t, uint64(10), eth0.RXPps)
	assert.Equal(t, uint64(1), eth0.TXPps)
}

func TestSystemNetworkHandler_LoopbackIncludeFlag(t *testing.T) {
	netDevCacheMu.Lock()
	netDevCache = map[string]netDevSample{}
	netDevCacheMu.Unlock()

	dir := t.TempDir()
	path := filepath.Join(dir, "net-dev")
	require.NoError(t, os.WriteFile(path, []byte(procNetDevSample), 0o644))
	t.Cleanup(func(orig string) func() {
		return func() { netDevPath = orig }
	}(netDevPath))
	netDevPath = path

	resp, err := systemNetworkHandler(context.Background(), json.RawMessage(`{"include_loopback":true}`))
	require.NoError(t, err)
	out := resp.(SystemNetworkResponse)
	gotLo := false
	for _, iface := range out.Interfaces {
		if iface.Iface == "lo" {
			gotLo = true
		}
	}
	assert.True(t, gotLo, "loopback must appear when include_loopback=true")
}
