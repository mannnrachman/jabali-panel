// Package sessionwatcher polls /usr/local/maldetect/sess/ for new
// session.<id> files written by maldet on each scan/quarantine.
// Parses each new session and dispatches a security.malware.event
// payload via the agent command registry (loopback POST to panel-api).
//
// Polling instead of fsnotify so the agent does not pick up a new
// runtime dependency. 5-second tick is well under the typical inotify
// scan cadence; we cache the last-seen mtime per file so re-runs are
// idempotent.
package sessionwatcher

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionDir is the LMD session directory; overridable for tests.
const SessionDir = "/usr/local/maldetect/sess"

// Hit is one quarantined file extracted from a maldet session file.
type Hit struct {
	OriginalPath   string
	QuarantinePath string
	Signature      string
	SHA256         string
	SizeBytes      int64
}

// Session is a parsed session.<id> file.
type Session struct {
	ID         string
	StartedAt  time.Time
	TotalFiles int
	TotalHits  int
	Hits       []Hit
	Raw        string // raw text for raw_json forensics
}

// Dispatcher is invoked once per fresh session.<id> file. The agent
// passes a closure that calls security.malware.event with the
// MalwareEventIngest payload.
type Dispatcher func(ctx context.Context, sess Session) error

// Run blocks polling SessionDir until ctx is cancelled. Caller wires
// the goroutine. Errors are logged, never returned (resilience over
// strict error propagation — a transient ENOENT during install must
// not crash the agent).
func Run(ctx context.Context, log *slog.Logger, dispatch Dispatcher) {
	if log == nil {
		log = slog.Default()
	}
	seen := loadSeen(log)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			scanOnce(ctx, log, seen, dispatch)
		}
	}
}

// loadSeen primes the seen map with current mtimes so the agent does
// not re-dispatch every historical session on first start. Operators
// who want a backfill can wipe the cache by deleting
// /var/lib/jabali-agent/sessionwatcher.json.
func loadSeen(log *slog.Logger) map[string]time.Time {
	seen := map[string]time.Time{}
	const cachePath = "/var/lib/jabali-agent/sessionwatcher.json"
	if data, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	entries, err := os.ReadDir(SessionDir)
	if err != nil {
		// First run before maldet installed — empty map is fine.
		log.Debug("sessionwatcher: prime read failed", "err", err)
		return seen
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "session.") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if _, ok := seen[e.Name()]; !ok {
			seen[e.Name()] = info.ModTime()
		}
	}
	return seen
}

func saveSeen(seen map[string]time.Time) {
	const cachePath = "/var/lib/jabali-agent/sessionwatcher.json"
	if err := os.MkdirAll(filepath.Dir(cachePath), 0750); err != nil {
		return
	}
	data, err := json.Marshal(seen)
	if err != nil {
		return
	}
	tmp := cachePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return
	}
	_ = os.Rename(tmp, cachePath)
}

func scanOnce(ctx context.Context, log *slog.Logger, seen map[string]time.Time, dispatch Dispatcher) {
	entries, err := os.ReadDir(SessionDir)
	if err != nil {
		// Quietly retry — directory may not exist yet on a fresh box.
		return
	}
	type candidate struct {
		name string
		mod  time.Time
	}
	var fresh []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "session.") {
			continue
		}
		// Skip the symlink session.last; we want the real session files.
		if e.Name() == "session.last" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		prev, ok := seen[e.Name()]
		if ok && !info.ModTime().After(prev) {
			continue
		}
		fresh = append(fresh, candidate{name: e.Name(), mod: info.ModTime()})
	}
	if len(fresh) == 0 {
		return
	}
	// Process in mtime order so events arrive chronologically.
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].mod.Before(fresh[j].mod) })

	for _, c := range fresh {
		full := filepath.Join(SessionDir, c.name)
		data, err := os.ReadFile(full)
		if err != nil {
			log.Warn("sessionwatcher: read session", "file", c.name, "err", err)
			continue
		}
		sess := Parse(string(data))
		if sess.ID == "" {
			sess.ID = strings.TrimPrefix(c.name, "session.")
		}
		if sess.StartedAt.IsZero() {
			sess.StartedAt = c.mod
		}
		if err := dispatch(ctx, sess); err != nil {
			log.Warn("sessionwatcher: dispatch failed", "session", sess.ID, "err", err)
			// Do NOT mark seen on dispatch failure — retry next tick.
			continue
		}
		seen[c.name] = c.mod
	}
	saveSeen(seen)
}
