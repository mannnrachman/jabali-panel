package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// SystemInfoResponse is the payload for system.info.
type SystemInfoResponse struct {
	Hostname      string          `json:"hostname"`
	Timezone      string          `json:"timezone"`
	UptimeSeconds float64         `json:"uptime_seconds"`
	LoadAvg       [3]float64      `json:"load_avg"`
	CPUCount      int             `json:"cpu_count"`
	MemTotalKB    uint64          `json:"mem_total_kb"`
	MemAvailKB    uint64          `json:"mem_available_kb"`
	MemUsedKB     uint64          `json:"mem_used_kb"`
	Partitions    []PartitionInfo `json:"partitions"`
}

// PartitionInfo describes one mounted filesystem.
type PartitionInfo struct {
	MountPoint string `json:"mount_point"`
	TotalBytes uint64 `json:"total_bytes"`
	UsedBytes  uint64 `json:"used_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

// procRoot is the base for /proc reads. Tests override it to point at
// fixture directories.
var procRoot = "/proc"

func systemInfoHandler(_ context.Context, _ json.RawMessage) (any, error) {
	hostname, _ := os.Hostname()

	uptime, err := parseUptime(procRoot + "/uptime")
	if err != nil {
		return nil, fmt.Errorf("read uptime: %w", err)
	}

	loadAvg, err := parseLoadAvg(procRoot + "/loadavg")
	if err != nil {
		return nil, fmt.Errorf("read loadavg: %w", err)
	}

	cpuCount, err := parseCPUCount(procRoot + "/cpuinfo")
	if err != nil {
		cpuCount = 1 // fallback
	}

	memTotal, memAvail, err := parseMeminfo(procRoot + "/meminfo")
	if err != nil {
		return nil, fmt.Errorf("read meminfo: %w", err)
	}

	mounts := []string{"/", "/home", "/var", "/tmp"}
	partitions := collectPartitions(mounts)

	return SystemInfoResponse{
		Hostname:      hostname,
		Timezone:      readSystemTimezone(),
		UptimeSeconds: uptime,
		LoadAvg:       loadAvg,
		CPUCount:      cpuCount,
		MemTotalKB:    memTotal,
		MemAvailKB:    memAvail,
		MemUsedKB:     memTotal - memAvail,
		Partitions:    partitions,
	}, nil
}

// readSystemTimezone returns the OS-configured IANA timezone (e.g.
// "Europe/Berlin"). Tries /etc/timezone (Debian/Ubuntu standard) first,
// then resolves the /etc/localtime symlink ("/usr/share/zoneinfo/Europe/Berlin"
// → "Europe/Berlin"). Returns "" if neither method works — caller treats
// that as "OS default, don't push back to set_timezone."
func readSystemTimezone() string {
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(data)); tz != "" {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		const prefix = "/usr/share/zoneinfo/"
		if i := strings.Index(target, prefix); i >= 0 {
			return target[i+len(prefix):]
		}
	}
	return ""
}

// parseUptime reads /proc/uptime → "12345.67 98765.43\n"
func parseUptime(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return ParseUptimeData(string(data))
}

// ParseUptimeData extracts uptime seconds from /proc/uptime content.
// Exported for testing.
func ParseUptimeData(content string) (float64, error) {
	fields := strings.Fields(content)
	if len(fields) < 1 {
		return 0, fmt.Errorf("unexpected uptime format")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// parseLoadAvg reads /proc/loadavg → "0.15 0.10 0.05 1/234 5678\n"
func parseLoadAvg(path string) ([3]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [3]float64{}, err
	}
	return ParseLoadAvgData(string(data))
}

// ParseLoadAvgData extracts 1/5/15 min load averages. Exported for testing.
func ParseLoadAvgData(content string) ([3]float64, error) {
	fields := strings.Fields(content)
	if len(fields) < 3 {
		return [3]float64{}, fmt.Errorf("unexpected loadavg format")
	}
	var out [3]float64
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return [3]float64{}, fmt.Errorf("parse loadavg field %d: %w", i, err)
		}
		out[i] = v
	}
	return out, nil
}

// parseCPUCount counts "processor" lines in /proc/cpuinfo.
func parseCPUCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return ParseCPUCountData(string(data)), nil
}

// ParseCPUCountData counts processor lines. Exported for testing.
func ParseCPUCountData(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "processor") {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

// parseMeminfo reads MemTotal and MemAvailable from /proc/meminfo.
func parseMeminfo(path string) (total, available uint64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	return ParseMeminfoData(string(data))
}

// ParseMeminfoData parses /proc/meminfo content. Exported for testing.
func ParseMeminfoData(content string) (total, available uint64, err error) {
	var gotTotal, gotAvail bool
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			total, err = parseMeminfoLine(line)
			if err != nil {
				return 0, 0, err
			}
			gotTotal = true
		} else if strings.HasPrefix(line, "MemAvailable:") {
			available, err = parseMeminfoLine(line)
			if err != nil {
				return 0, 0, err
			}
			gotAvail = true
		}
		if gotTotal && gotAvail {
			break
		}
	}
	if !gotTotal {
		return 0, 0, fmt.Errorf("MemTotal not found in meminfo")
	}
	if !gotAvail {
		return 0, 0, fmt.Errorf("MemAvailable not found in meminfo")
	}
	return total, available, nil
}

// parseMeminfoLine extracts KB value from "MemTotal:  12345 kB"
func parseMeminfoLine(line string) (uint64, error) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, fmt.Errorf("bad meminfo line: %q", line)
	}
	return strconv.ParseUint(parts[1], 10, 64)
}

// collectPartitions runs statfs on each mount point and returns info for
// those that exist and are real filesystems (total > 0). Deduplicates by
// total+free size which is good enough for the handful of mounts we probe.
func collectPartitions(mounts []string) []PartitionInfo {
	type key struct{ total, free uint64 }
	seen := map[key]bool{}
	var out []PartitionInfo
	for _, mp := range mounts {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(mp, &stat); err != nil {
			continue
		}
		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)
		if total == 0 {
			continue
		}
		k := key{total, free}
		if seen[k] && mp != "/" {
			continue
		}
		seen[k] = true
		out = append(out, PartitionInfo{
			MountPoint: mp,
			TotalBytes: total,
			FreeBytes:  free,
			UsedBytes:  total - free,
		})
	}
	return out
}

func init() {
	Default.Register("system.info", systemInfoHandler)
}
