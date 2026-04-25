package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// SystemCPUUsageResponse holds aggregate + per-core busy% over the
// sampling window. First call after agent boot returns zeros + warming_up
// because we have no prior /proc/stat sample to delta against.
type SystemCPUUsageResponse struct {
	UsagePercent  float64   `json:"usage_percent"`
	IOWaitPercent float64   `json:"iowait_percent"`
	PerCore       []float64 `json:"per_core"`
	WarmingUp     bool      `json:"warming_up"`
	AsOf          string    `json:"as_of"`
}

// cpuStatSample is one /proc/stat aggregate row in user-set columns.
// Only the seven we use are stored.
type cpuStatSample struct {
	user      uint64
	nice      uint64
	system    uint64
	idle      uint64
	iowait    uint64
	irq       uint64
	softirq   uint64
	steal     uint64
	at        time.Time
}

func (s cpuStatSample) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s cpuStatSample) busy() uint64 {
	return s.user + s.nice + s.system + s.irq + s.softirq + s.steal
}

var (
	cpuStatPath  = "/proc/stat"
	cpuStatCache = struct {
		mu      sync.Mutex
		agg     cpuStatSample // last "cpu" aggregate row
		percore []cpuStatSample
		hasPrev bool
	}{}
)

func systemCPUUsageHandler(_ context.Context, _ json.RawMessage) (any, error) {
	data, err := os.ReadFile(cpuStatPath)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read %s: %v", cpuStatPath, err)}
	}
	now := time.Now()
	agg, percore := parseProcStat2(string(data), now)

	cpuStatCache.mu.Lock()
	defer cpuStatCache.mu.Unlock()

	resp := SystemCPUUsageResponse{
		AsOf:    now.UTC().Format(time.RFC3339Nano),
		PerCore: make([]float64, len(percore)),
	}

	if !cpuStatCache.hasPrev {
		resp.WarmingUp = true
	} else {
		resp.UsagePercent = busyPercent(cpuStatCache.agg, agg)
		resp.IOWaitPercent = iowaitPercent(cpuStatCache.agg, agg)
		for i, curr := range percore {
			if i < len(cpuStatCache.percore) {
				resp.PerCore[i] = busyPercent(cpuStatCache.percore[i], curr)
			}
		}
	}

	cpuStatCache.agg = agg
	cpuStatCache.percore = percore
	cpuStatCache.hasPrev = true
	return resp, nil
}

// parseProcStat2 reads /proc/stat. The first row "cpu  ..." is the
// aggregate; rows "cpuN ..." are per-core. We only consume the first
// 8 numeric columns (user .. steal) — anything past that is unused.
func parseProcStat2(content string, at time.Time) (agg cpuStatSample, percore []cpuStatSample) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		s := cpuStatSample{at: at}
		s.user, _ = strconv.ParseUint(fields[1], 10, 64)
		s.nice, _ = strconv.ParseUint(fields[2], 10, 64)
		s.system, _ = strconv.ParseUint(fields[3], 10, 64)
		s.idle, _ = strconv.ParseUint(fields[4], 10, 64)
		s.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
		s.irq, _ = strconv.ParseUint(fields[6], 10, 64)
		s.softirq, _ = strconv.ParseUint(fields[7], 10, 64)
		s.steal, _ = strconv.ParseUint(fields[8], 10, 64)
		if fields[0] == "cpu" {
			agg = s
		} else {
			percore = append(percore, s)
		}
	}
	return agg, percore
}

func busyPercent(prev, curr cpuStatSample) float64 {
	totalDelta := curr.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}
	busyDelta := curr.busy() - prev.busy()
	return float64(busyDelta) * 100.0 / float64(totalDelta)
}

func iowaitPercent(prev, curr cpuStatSample) float64 {
	totalDelta := curr.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}
	iowaitDelta := curr.iowait - prev.iowait
	return float64(iowaitDelta) * 100.0 / float64(totalDelta)
}

func init() {
	Default.Register("system.cpu_usage", systemCPUUsageHandler)
}
