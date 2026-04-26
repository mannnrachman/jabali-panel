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

// fakeUserSliceTree builds a minimal /sys/fs/cgroup/jabali.slice/jabali-user.slice
// layout under tmp with two users + their cpu.stat / memory.* / pids.current files.
func fakeUserSliceTree(t *testing.T, users map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for u, files := range users {
		dir := filepath.Join(root, "jabali-user-"+u+".slice")
		require.NoError(t, os.MkdirAll(dir, 0755))
		for fname, content := range files {
			require.NoError(t, os.WriteFile(filepath.Join(dir, fname), []byte(content), 0644))
		}
	}
	return root
}

func TestSystemUserSlices_WarmingUpFirstCall(t *testing.T) {
	root := fakeUserSliceTree(t, map[string]map[string]string{
		"alice": {
			"cpu.stat":       "usage_usec 1000000\nuser_usec 500000\nsystem_usec 500000\n",
			"memory.current": "10485760\n",
			"memory.max":     "max\n",
			"pids.current":   "5\n",
		},
	})

	t.Cleanup(func() {
		userSliceCache.mu.Lock()
		userSliceCache.hasPrev = false
		userSliceCache.last = map[string]userSliceCPUSample{}
		userSliceCache.mu.Unlock()
	})
	userSliceCache.mu.Lock()
	userSliceCache.hasPrev = false
	userSliceCache.last = map[string]userSliceCPUSample{}
	userSliceCache.mu.Unlock()

	origRoot, origClock := userSliceCgroupRoot, userSliceClock
	t.Cleanup(func() {
		userSliceCgroupRoot = origRoot
		userSliceClock = origClock
	})
	userSliceCgroupRoot = root
	userSliceClock = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }

	resp, err := systemUserSlicesHandler(context.Background(), nil)
	require.NoError(t, err)
	r := resp.(SystemUserSlicesResponse)
	assert.True(t, r.WarmingUp)
	require.Len(t, r.Slices, 1)
	assert.Equal(t, "alice", r.Slices[0].Username)
	assert.Equal(t, uint64(10485760), r.Slices[0].MemoryBytes)
	assert.Equal(t, uint64(0), r.Slices[0].MemoryMaxBytes, "max → 0")
	assert.Equal(t, 5, r.Slices[0].Tasks)
	assert.Equal(t, 0.0, r.Slices[0].CPUPercent, "first sample has no delta")
}

func TestSystemUserSlices_CPUDelta(t *testing.T) {
	root := fakeUserSliceTree(t, map[string]map[string]string{
		"bob": {
			"cpu.stat":       "usage_usec 1000000\n",
			"memory.current": "0\n",
			"memory.max":     "max\n",
			"pids.current":   "1\n",
		},
	})

	t.Cleanup(func() {
		userSliceCache.mu.Lock()
		userSliceCache.hasPrev = false
		userSliceCache.last = map[string]userSliceCPUSample{}
		userSliceCache.mu.Unlock()
	})
	userSliceCache.mu.Lock()
	userSliceCache.hasPrev = false
	userSliceCache.last = map[string]userSliceCPUSample{}
	userSliceCache.mu.Unlock()

	origRoot, origClock := userSliceCgroupRoot, userSliceClock
	t.Cleanup(func() {
		userSliceCgroupRoot = origRoot
		userSliceClock = origClock
	})
	userSliceCgroupRoot = root

	t0 := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	userSliceClock = func() time.Time { return t0 }
	_, err := systemUserSlicesHandler(context.Background(), nil)
	require.NoError(t, err)

	// Second call: 5s later, usage_usec advanced by 500000 (= 0.5 CPU-seconds
	// over 5s wallclock window = 10% busy).
	require.NoError(t, os.WriteFile(filepath.Join(root, "jabali-user-bob.slice", "cpu.stat"),
		[]byte("usage_usec 1500000\n"), 0644))
	userSliceClock = func() time.Time { return t0.Add(5 * time.Second) }

	resp, err := systemUserSlicesHandler(context.Background(), nil)
	require.NoError(t, err)
	r := resp.(SystemUserSlicesResponse)
	assert.False(t, r.WarmingUp)
	require.Len(t, r.Slices, 1)
	assert.InDelta(t, 10.0, r.Slices[0].CPUPercent, 0.01)
}

func TestSystemUserSlices_MissingRootReturnsEmpty(t *testing.T) {
	origRoot := userSliceCgroupRoot
	t.Cleanup(func() { userSliceCgroupRoot = origRoot })
	userSliceCgroupRoot = "/nonexistent/jabali.slice/jabali-user.slice"

	resp, err := systemUserSlicesHandler(context.Background(), nil)
	require.NoError(t, err)
	r := resp.(SystemUserSlicesResponse)
	assert.Empty(t, r.Slices)
}
