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
}

// ProcessTopEntry is one row of the top-N-by-RSS list. CPU% is omitted
// in v1 — computing it correctly needs two /proc/<pid>/stat samples
// over an interval and the agent doesn't keep that state per pid yet.
// We surface RSS and command, which are the fields operators actually
// scan first.
type ProcessTopEntry struct {
	PID    int    `json:"pid"`
	Comm   string `json:"comm"`
	User   string `json:"user"`
	RSSKB  uint64 `json:"rss_kb"`
	State  string `json:"state"`
}

const defaultTopN = 10

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

func systemProcessesHandler(_ context.Context, _ json.RawMessage) (any, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read %s: %v", procDir, err)}
	}
	uids := newUIDLookup()
	var procs []ProcessTopEntry
	resp := SystemProcessesResponse{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		info, err := readProcInfo(procDir, pid, uids)
		if err != nil {
			// Process exited between readdir and our open — common,
			// not an error.
			continue
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

	sort.Slice(procs, func(i, j int) bool { return procs[i].RSSKB > procs[j].RSSKB })
	if len(procs) > defaultTopN {
		procs = procs[:defaultTopN]
	}
	resp.TopByRSS = procs
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

func init() {
	Default.Register("system.processes", systemProcessesHandler)
}
