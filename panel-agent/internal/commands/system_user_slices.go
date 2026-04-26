// system_user_slices — M18 per-user cgroup v2 slice metrics for the
// Server Status page.
//
// Each Linux user managed by the panel runs inside
// /sys/fs/cgroup/jabali.slice/jabali-user.slice/jabali-user-<u>.slice
// (see ADR-0032 + user_slice_ensure.go). This handler enumerates those
// slice directories, reads cpu.stat / memory.current / memory.max /
// pids.current, and returns one row per user. CPU% is computed from the
// usage_usec delta against the previous sample (warming_up=true on
// first call after agent boot, mirroring system.cpu_usage).

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// UserSliceMetric is one row in the response — a single user's slice
// snapshot. CPUPercent is averaged over the wallclock interval since
// the previous sample.
type UserSliceMetric struct {
	Username      string  `json:"username"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryBytes   uint64  `json:"memory_bytes"`
	MemoryMaxBytes uint64 `json:"memory_max_bytes"` // 0 == "max" (unlimited)
	Tasks         int     `json:"tasks"`
}

// SystemUserSlicesResponse is the wire payload.
type SystemUserSlicesResponse struct {
	Slices    []UserSliceMetric `json:"slices"`
	WarmingUp bool              `json:"warming_up"`
	AsOf      string            `json:"as_of"`
}

// userSliceCgroupRoot is overridable for tests.
var userSliceCgroupRoot = "/sys/fs/cgroup/jabali.slice/jabali-user.slice"

// userSliceClock is overridable for deterministic delta computation.
var userSliceClock = time.Now

// userSliceCache holds the previous CPU sample per user so we can
// compute % over the wallclock interval. Same pattern as system_cpu_usage
// + system_network. Survives across handler invocations only — agent
// restart resets it (warming_up=true on next call).
var userSliceCache = struct {
	mu       sync.Mutex
	last     map[string]userSliceCPUSample
	lastWhen time.Time
	hasPrev  bool
}{
	last: map[string]userSliceCPUSample{},
}

type userSliceCPUSample struct {
	usageUsec uint64
}

func systemUserSlicesHandler(_ context.Context, _ json.RawMessage) (any, error) {
	now := userSliceClock()

	entries, err := os.ReadDir(userSliceCgroupRoot)
	if err != nil {
		// On hosts where the parent slice doesn't exist yet (no users
		// provisioned), return an empty slice rather than erroring out
		// — the aggregator should still ship a usable envelope.
		if os.IsNotExist(err) {
			return SystemUserSlicesResponse{
				Slices: []UserSliceMetric{},
				AsOf:   now.UTC().Format(time.RFC3339Nano),
			}, nil
		}
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read %s: %v", userSliceCgroupRoot, err)}
	}

	userSliceCache.mu.Lock()
	defer userSliceCache.mu.Unlock()

	intervalSecs := 0.0
	if userSliceCache.hasPrev {
		intervalSecs = now.Sub(userSliceCache.lastWhen).Seconds()
	}

	resp := SystemUserSlicesResponse{
		Slices: []UserSliceMetric{},
		AsOf:   now.UTC().Format(time.RFC3339Nano),
	}
	if !userSliceCache.hasPrev {
		resp.WarmingUp = true
	}

	current := map[string]userSliceCPUSample{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		const prefix = "jabali-user-"
		const suffix = ".slice"
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		username := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)

		row := UserSliceMetric{Username: username}
		dir := filepath.Join(userSliceCgroupRoot, name)

		// usage_usec — total CPU microseconds since slice start. Delta
		// over our wallclock interval gives the busy% over the window.
		usage := readCgroupCPUUsageUsec(filepath.Join(dir, "cpu.stat"))
		current[username] = userSliceCPUSample{usageUsec: usage}
		if userSliceCache.hasPrev && intervalSecs > 0 {
			if prev, ok := userSliceCache.last[username]; ok && usage >= prev.usageUsec {
				delta := usage - prev.usageUsec
				row.CPUPercent = (float64(delta) / 1e6) / intervalSecs * 100.0
			}
		}

		row.MemoryBytes = readCgroupUint(filepath.Join(dir, "memory.current"))
		// memory.max returns "max" when no limit; treat as 0.
		row.MemoryMaxBytes = readCgroupMemoryMax(filepath.Join(dir, "memory.max"))
		row.Tasks = int(readCgroupUint(filepath.Join(dir, "pids.current")))

		resp.Slices = append(resp.Slices, row)
	}

	userSliceCache.last = current
	userSliceCache.lastWhen = now
	userSliceCache.hasPrev = true

	return resp, nil
}

// readCgroupCPUUsageUsec parses cpu.stat for the usage_usec line. cpu.stat
// is a key-value file: "usage_usec 1234\nuser_usec 1000\n...".
func readCgroupCPUUsageUsec(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "usage_usec ") {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "usage_usec ")), 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	return 0
}

// readCgroupUint reads a single-line cgroup file holding a uint64.
// memory.current, pids.current both follow this shape.
func readCgroupUint(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// readCgroupMemoryMax parses memory.max which holds either a uint64 or
// the literal string "max" (no limit). "max" → 0 (UI renders as "—").
func readCgroupMemoryMax(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(b))
	if s == "max" || s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func init() {
	Default.Register("system.user_slices", systemUserSlicesHandler)
}
