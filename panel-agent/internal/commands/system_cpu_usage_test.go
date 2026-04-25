package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const procStatT0 = `cpu  100 0 50 1000 5 0 0 0
cpu0 50 0 25 500 5 0 0 0
cpu1 50 0 25 500 0 0 0 0
intr 12345
ctxt 67890
`
const procStatT1 = `cpu  200 0 100 1000 10 0 0 0
cpu0 100 0 50 500 10 0 0 0
cpu1 100 0 50 500 0 0 0 0
intr 12500
ctxt 68000
`

func TestParseProcStat2_HappyPath(t *testing.T) {
	now := time.Now()
	agg, percore := parseProcStat2(procStatT0, now)
	assert.Equal(t, uint64(100), agg.user)
	assert.Equal(t, uint64(1000), agg.idle)
	require.Len(t, percore, 2)
	assert.Equal(t, uint64(50), percore[0].user)
}

func TestBusyPercent(t *testing.T) {
	prev := cpuStatSample{user: 100, idle: 1000}
	curr := cpuStatSample{user: 200, idle: 1000}
	// total delta = 100 (user) + 0 (idle) = 100; busy delta = 100;
	// → 100% busy.
	assert.InDelta(t, 100.0, busyPercent(prev, curr), 0.01)
}

func TestSystemCPUUsageHandler_FirstSampleWarmsUp(t *testing.T) {
	cpuStatCache.mu.Lock()
	cpuStatCache.hasPrev = false
	cpuStatCache.percore = nil
	cpuStatCache.mu.Unlock()

	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	require.NoError(t, os.WriteFile(path, []byte(procStatT0), 0o644))
	t.Cleanup(func(orig string) func() { return func() { cpuStatPath = orig } }(cpuStatPath))
	cpuStatPath = path

	resp, err := systemCPUUsageHandler(context.Background(), nil)
	require.NoError(t, err)
	out := resp.(SystemCPUUsageResponse)
	assert.True(t, out.WarmingUp)
	assert.Equal(t, 0.0, out.UsagePercent)
}

func TestSystemCPUUsageHandler_SecondSampleHasNumbers(t *testing.T) {
	cpuStatCache.mu.Lock()
	cpuStatCache.hasPrev = false
	cpuStatCache.percore = nil
	cpuStatCache.mu.Unlock()

	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	t.Cleanup(func(orig string) func() { return func() { cpuStatPath = orig } }(cpuStatPath))
	cpuStatPath = path

	require.NoError(t, os.WriteFile(path, []byte(procStatT0), 0o644))
	_, err := systemCPUUsageHandler(context.Background(), nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte(procStatT1), 0o644))
	resp, err := systemCPUUsageHandler(context.Background(), nil)
	require.NoError(t, err)
	out := resp.(SystemCPUUsageResponse)

	// total delta agg: user 100 + system 50 + iowait 5 = busy 150,
	// idle 0 + iowait 5; total = 155 → busy% ≈ 96.77.
	assert.False(t, out.WarmingUp)
	assert.Greater(t, out.UsagePercent, 90.0)
	assert.Less(t, out.UsagePercent, 100.0)
	require.Len(t, out.PerCore, 2)
	assert.Greater(t, out.PerCore[0], 0.0)
}
