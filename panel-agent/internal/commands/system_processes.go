package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// SystemProcessesResponse is the payload for system.processes.
type SystemProcessesResponse struct {
	Total    int               `json:"total"`
	Running  int               `json:"running"`
	Sleeping int               `json:"sleeping"`
	Zombie   int               `json:"zombie"`
	Stopped  int               `json:"stopped"`
	Other    int               `json:"other"`
	TopByRSS []ProcessTopEntry `json:"top_by_rss"`
	TopByCPU []ProcessTopEntry `json:"top_by_cpu"`
}

// ProcessTopEntry is one row of the top-N list. CPU% is computed from
// two /proc/<pid>/stat samples taken `cpuSampleInterval` apart inside
// the same call — no per-call state in the agent. RSS comes from
// /proc/<pid>/statm. CPUPercent is normalized to the host's CPU count
// so a single fully-pegged core on an 8-core box reads as ~12.5%.
type ProcessTopEntry struct {
	PID        int     `json:"pid"`
	Comm       string  `json:"comm"`
	User       string  `json:"user"`
	RSSKB      uint64  `json:"rss_kb"`
	State      string  `json:"state"`
	CPUPercent float64 `json:"cpu_percent"`
}

const (
	defaultTopN        = 10
	cpuSampleInterval  = 200 * time.Millisecond
)

// procDir is the base for /proc reads. Tests override.
var procDir = "/proc"

// userCache memoizes uid → username lookups for the duration of one
// scan. Real hosts have a few hundred procs, mostly under 5-10 distinct
// uids; doing a getpwuid per pid is wasteful.
type uidLookup struct {
	mu    sync.Mutex
	cache map[uint32]string
}

func newUIDLookup() *uidLookup {
	return &uidLookup{cache: make(map[uint32]string)}
}

func (u *uidLookup) name(uid uint32) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if name, ok := u.cache[uid]; ok {
		return name
	}
	usr, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil || usr == nil {
		// Fall back to numeric uid — better debug signal than "unknown".
		name := strconv.FormatUint(uint64(uid), 10)
		u.cache[uid] = name
		return name
	}
	u.cache[uid] = usr.Username
	return usr.Username
}

// procSample captures the per-pid CPU jiffies snapshot taken at time t.
// Two such samples taken cpuSampleInterval apart let us compute the
// per-process CPU% without keeping any state across handler calls.
type procSample struct {
	utime  uint64
	stime  uint64
	atTime time.Time
}

func systemProcessesHandler(_ context.Context, _ json.RawMessage) (any, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read %s: %v", procDir, err)}
	}

	// First pass — collect pid set + sample 1 jiffies for every pid we
	// can read. CPU sample is best-effort; pids whose stat is too short
	// to parse jiffies (or doesn't exist) still get included with
	// cpu_percent=0 so a fixture-style /proc tree with truncated stat
	// files still reports population stats.
	uids := newUIDLookup()
	pids := []int{}
	sample1 := map[int]procSample{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		pids = append(pids, pid)
		if s, ok := readProcCPUSample(procDir, pid); ok {
			sample1[pid] = s
		}
	}

	// Sleep one sampling window then take sample 2 + the rest of the
	// per-process info (RSS, state, comm, uid).
	time.Sleep(cpuSampleInterval)

	resp := SystemProcessesResponse{}
	procs := make([]ProcessTopEntry, 0, len(pids))
	clkTck := readClkTck()
	cpuCount := readCPUCount(procDir + "/cpuinfo")
	if cpuCount < 1 {
		cpuCount = 1
	}
	for _, pid := range pids {
		info, err := readProcInfo(procDir, pid, uids)
		if err != nil {
			continue
		}
		if s2, ok := readProcCPUSample(procDir, pid); ok {
			s1, hasSample1 := sample1[pid]
			deltaJiffies := float64((s2.utime + s2.stime) - (s1.utime + s1.stime))
			deltaSec := s2.atTime.Sub(s1.atTime).Seconds()
			if hasSample1 && clkTck > 0 && deltaSec > 0 {
				// Per-core CPU% then divide by cpuCount so a pegged
				// single thread on an 8-core box reads as ~12.5%, not
				// 100%.
				info.CPUPercent = (deltaJiffies / float64(clkTck)) / deltaSec * 100.0 / float64(cpuCount)
				if info.CPUPercent < 0 {
					info.CPUPercent = 0
				}
			}
		}
		resp.Total++
		switch info.State {
		case "R":
			resp.Running++
		case "S", "D", "I":
			// Sleeping covers interruptible (S), uninterruptible (D),
			// and idle (I). All three are "not currently runnable on
			// CPU" from the operator's perspective.
			resp.Sleeping++
		case "Z":
			resp.Zombie++
		case "T", "t":
			resp.Stopped++
		default:
			resp.Other++
		}
		procs = append(procs, info)
	}

	// Top-N by RSS.
	byRSS := make([]ProcessTopEntry, len(procs))
	copy(byRSS, procs)
	sort.Slice(byRSS, func(i, j int) bool { return byRSS[i].RSSKB > byRSS[j].RSSKB })
	if len(byRSS) > defaultTopN {
		byRSS = byRSS[:defaultTopN]
	}
	resp.TopByRSS = byRSS

	// Top-N by CPU%. Stable secondary by RSS so two zero-CPU rows have
	// a deterministic order (helps the UI not jitter when pollers
	// converge).
	byCPU := make([]ProcessTopEntry, len(procs))
	copy(byCPU, procs)
	sort.Slice(byCPU, func(i, j int) bool {
		if byCPU[i].CPUPercent != byCPU[j].CPUPercent {
			return byCPU[i].CPUPercent > byCPU[j].CPUPercent
		}
		return byCPU[i].RSSKB > byCPU[j].RSSKB
	})
	if len(byCPU) > defaultTopN {
		byCPU = byCPU[:defaultTopN]
	}
	resp.TopByCPU = byCPU

	return resp, nil
}

// readProcInfo pulls the four fields we need: comm + state from
// /proc/<pid>/stat (which is also faster + atomic than /proc/<pid>/status),
// RSS from /proc/<pid>/statm, and uid from /proc/<pid>/status. Returns
// an error if the pid disappeared mid-read (zero-cost retry: caller
// just skips it).
func readProcInfo(base string, pid int, uids *uidLookup) (ProcessTopEntry, error) {
	pidStr := strconv.Itoa(pid)

	statRaw, err := os.ReadFile(base + "/" + pidStr + "/stat")
	if err != nil {
		return ProcessTopEntry{}, err
	}
	comm, state, err := parseProcStat(string(statRaw))
	if err != nil {
		return ProcessTopEntry{}, err
	}

	statmRaw, err := os.ReadFile(base + "/" + pidStr + "/statm")
	if err != nil {
		return ProcessTopEntry{}, err
	}
	rssKB, err := parseProcStatm(string(statmRaw))
	if err != nil {
		return ProcessTopEntry{}, err
	}

	uidVal := readProcStatusUID(base, pidStr)

	return ProcessTopEntry{
		PID:   pid,
		Comm:  comm,
		User:  uids.name(uidVal),
		RSSKB: rssKB,
		State: state,
	}, nil
}

// readProcCPUSample reads /proc/<pid>/stat and returns utime+stime
// jiffies plus the wall-clock time the read happened. Returns false
// when the pid vanished mid-call.
func readProcCPUSample(base string, pid int) (procSample, bool) {
	raw, err := os.ReadFile(base + "/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procSample{}, false
	}
	utime, stime, ok := parseProcStatCPU(string(raw))
	if !ok {
		return procSample{}, false
	}
	return procSample{utime: utime, stime: stime, atTime: time.Now()}, true
}

// parseProcStatCPU pulls fields 14 (utime) + 15 (stime) from
// /proc/<pid>/stat. comm in field 2 may contain spaces, so split on
// the LAST ')' first.
func parseProcStatCPU(content string) (utime, stime uint64, ok bool) {
	close := strings.LastIndexByte(content, ')')
	if close < 0 {
		return 0, 0, false
	}
	rest := strings.TrimSpace(content[close+1:])
	fields := strings.Fields(rest)
	// state(1) ppid(2) pgrp(3) session(4) tty(5) tpgid(6) flags(7)
	// minflt(8) cminflt(9) majflt(10) cmajflt(11) utime(12) stime(13)
	// (after the trailing-paren split, fields are 0-indexed: utime=11, stime=12)
	if len(fields) < 13 {
		return 0, 0, false
	}
	u, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	s, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return u, s, true
}

// readClkTck returns the kernel's CLOCKS_PER_SEC (ticks/sec). Linux
// almost always exports 100. We hardcode the fallback for safety —
// /proc doesn't expose it directly so syscall would be the proper
// path, but we're inside a goroutine and don't want a libc dance.
var clkTckOverride int64 = 0

func readClkTck() int64 {
	if clkTckOverride > 0 {
		return clkTckOverride
	}
	return 100
}

// parseProcStat extracts comm + state from /proc/<pid>/stat. The format
// is: "<pid> (<comm>) <state> ..." but comm can contain spaces and
// parentheses, so we slice between the FIRST '(' and the LAST ')'.
func parseProcStat(content string) (comm, state string, err error) {
	open := strings.IndexByte(content, '(')
	close := strings.LastIndexByte(content, ')')
	if open < 0 || close < 0 || close <= open {
		return "", "", fmt.Errorf("bad stat: %q", content)
	}
	comm = content[open+1 : close]
	rest := strings.TrimSpace(content[close+1:])
	fields := strings.Fields(rest)
	if len(fields) < 1 {
		return "", "", fmt.Errorf("bad stat tail: %q", rest)
	}
	state = fields[0]
	return comm, state, nil
}

// parseProcStatm extracts RSS in pages then converts to KB. Format is:
//
//	"size resident shared text lib data dirty"
//
// — values in pages. PageSize=4096 on Linux x86_64; we use the syscall
// constant for portability against the rare arch with different pages.
func parseProcStatm(content string) (uint64, error) {
	fields := strings.Fields(content)
	if len(fields) < 2 {
		return 0, fmt.Errorf("bad statm: %q", content)
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * uint64(os.Getpagesize()) / 1024, nil
}

// readProcStatusUID pulls the real UID from /proc/<pid>/status's "Uid:"
// line ("Uid: <real> <effective> <saved> <fs>"). Returns 0 if the file
// is missing — the process may have exited.
func readProcStatusUID(base, pid string) uint32 {
	data, err := os.ReadFile(base + "/" + pid + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		v, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			return 0
		}
		return uint32(v)
	}
	return 0
}

// readCPUCount counts "processor" lines in /proc/cpuinfo. Defined here
// (not imported) so this package stays self-contained for tests.
func readCPUCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "processor") {
			count++
		}
	}
	if count < 1 {
		count = 1
	}
	return count
}

func init() {
	Default.Register("system.processes", systemProcessesHandler)
}
