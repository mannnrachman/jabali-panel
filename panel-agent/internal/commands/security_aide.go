// security_aide — M42 (ADR-0087) thin wrapper around AIDE. Two
// commands: status (read latest report + DB age) and check (manual
// trigger).
//
// AIDE's audit.log file IS the storage; we don't mirror events into
// MariaDB. Daily check via jabali-aide-check.timer; this surface is
// for the panel UI to peek between scheduled runs.

package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	osexec "os/exec"
	"os"
	"strings"
	"time"
)

const aideCallTimeout = 15 * time.Second

type aideStatusResponse struct {
	Enabled         bool   `json:"enabled"`
	DBAgeSeconds    int64  `json:"db_age_seconds"`
	LastCheckTS     string `json:"last_check_ts,omitempty"`
	Summary         struct {
		Added   int `json:"added"`
		Changed int `json:"changed"`
		Removed int `json:"removed"`
	} `json:"summary"`
	Sample []aideSampleRow `json:"sample"`
	// Reason: human-readable when Enabled=false (e.g. "AIDE not
	// installed", "DB still building").
	Reason string `json:"reason,omitempty"`
}

type aideSampleRow struct {
	Path       string `json:"path"`
	ChangeType string `json:"change_type"` // added|changed|removed
}

const (
	aideDBPath     = "/var/lib/aide/aide.db"
	aideMarkerPath = "/var/lib/aide/.jabali-installed"
	aideReportPath = "/var/log/aide/aide.report.log"
)

func mwAideStatusHandler(_ context.Context, _ json.RawMessage) (any, error) {
	resp := aideStatusResponse{Sample: []aideSampleRow{}}

	if _, err := os.Stat("/usr/bin/aide"); err != nil {
		resp.Enabled = false
		resp.Reason = "AIDE binary not found"
		return resp, nil
	}

	if st, err := os.Stat(aideDBPath); err == nil {
		resp.Enabled = true
		resp.DBAgeSeconds = int64(time.Since(st.ModTime()).Seconds())
	} else {
		resp.Enabled = false
		if _, e := os.Stat("/var/lib/aide/.init-in-progress"); e == nil {
			resp.Reason = "AIDE DB still building (initial init in progress)"
		} else {
			resp.Reason = "AIDE DB missing — run 'aide --init'"
		}
		return resp, nil
	}

	// Parse the last report — `aide --check` appends to aide.report.log
	// on every timer run. We tail the last block, look for the
	// "AIDE found differences" header + "added entries" / "changed
	// entries" / "removed entries" counters.
	if f, err := os.Open(aideReportPath); err == nil {
		defer f.Close()
		// Read whole file (capped at 4MB to avoid runaway).
		var data []byte
		buf := make([]byte, 4*1024*1024)
		n, _ := f.Read(buf)
		data = buf[:n]
		parseAideReport(string(data), &resp)
	}

	return resp, nil
}

// parseAideReport extracts summary counts + sample paths from an
// aide.report.log. AIDE 0.18 / 0.19 emit several plain-text report
// formats depending on report_url and report_summarize_changes.
// Variants seen in the wild include:
//
//   "Added entries:" section header, then lines like
//      f++++++++++++++++++++++ZZZZ : /etc/example
//      f++++++++++++++++++++++ZZZZ /etc/example
//   or just
//      added: /etc/example
//
//   "Changed entries:" section, lines like
//      f   ...mc.. .C..       : /etc/bar
//      f =....TS....C.....    /etc/bar
//
// Rather than coupling to a single layout, we classify each line by
// its leading token: a token of the form `^[fdlcbDpsSh][+\-=. ]{N}.*`
// (file/dir/link/etc with the attribute string) tells us what kind of
// change it is — `+++` = added, `---` = removed, anything else with
// `.`/`=`/`m`/`c`/`s` etc = changed. Section headers are still parsed
// when present; they're an additional signal, not the only one.
//
// Sample is capped at 50 rows.
func parseAideReport(text string, resp *aideStatusResponse) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var lastTimestamp string
	var section string // "added" | "removed" | "changed" | ""
	var summarySeen bool

	classifyAttr := func(attr string) string {
		// Attribute string is the leading "f++++++++...ZZZZ" token
		// (or with `f .....mc..C..` etc). All-`+` after the type
		// char => added. All-`-` => removed. Anything else => changed.
		body := attr[1:] // drop leading f/d/l/...
		if body == "" {
			return "changed"
		}
		allPlus := true
		allMinus := true
		for _, r := range body {
			if r != '+' && r != 'Z' {
				allPlus = false
			}
			if r != '-' && r != 'Z' {
				allMinus = false
			}
		}
		if allPlus && !allMinus {
			return "added"
		}
		if allMinus && !allPlus {
			return "removed"
		}
		return "changed"
	}

	pushSample := func(kind, path string) {
		// Skip lines without an absolute path (defensive — AIDE always
		// emits absolute paths for system-file rules but the report
		// can also include rule-name aliases at the top).
		if !strings.HasPrefix(path, "/") {
			return
		}
		resp.Sample = append(resp.Sample, aideSampleRow{
			Path:       path,
			ChangeType: kind,
		})
	}

	for scanner.Scan() {
		if len(resp.Sample) >= 50 {
			break
		}
		raw := scanner.Text()
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimLeft(line, " \t")

		// Start timestamp: "Start timestamp: 2026-04-30 04:30:42 +0000 …"
		if strings.HasPrefix(line, "Start timestamp:") {
			lastTimestamp = strings.TrimSpace(strings.TrimPrefix(line, "Start timestamp:"))
			continue
		}
		if strings.HasPrefix(trimmed, "Total number of entries:") {
			continue
		}

		// Summary counts. The first occurrence of each counter line
		// (in the Summary block) sets the count. Later occurrences of
		// the same prefix as a section header have NO numeric suffix
		// and won't match Sscanf — so we don't need to gate by section.
		if strings.HasPrefix(trimmed, "Added entries:") {
			var n int
			if _, err := fmt.Sscanf(trimmed, "Added entries:%d", &n); err == nil && !summarySeen {
				resp.Summary.Added = n
			} else {
				section = "added"
			}
			summarySeen = true
			continue
		}
		if strings.HasPrefix(trimmed, "Removed entries:") {
			var n int
			if _, err := fmt.Sscanf(trimmed, "Removed entries:%d", &n); err == nil && resp.Summary.Removed == 0 {
				resp.Summary.Removed = n
			} else {
				section = "removed"
			}
			continue
		}
		if strings.HasPrefix(trimmed, "Changed entries:") {
			var n int
			if _, err := fmt.Sscanf(trimmed, "Changed entries:%d", &n); err == nil && resp.Summary.Changed == 0 {
				resp.Summary.Changed = n
			} else {
				section = "changed"
			}
			continue
		}
		if trimmed == "Detailed information about changes:" {
			// Per-file detail block follows; don't try to mine paths
			// from it — the section list above gave us a clean sample.
			section = "detail"
			continue
		}

		// Skip horizontal rules + blanks.
		if trimmed == "" || strings.HasPrefix(trimmed, "---") {
			continue
		}
		// Skip per-file detail headers ("File: …").
		if strings.HasPrefix(trimmed, "File:") || strings.HasPrefix(trimmed, "Summary:") {
			continue
		}
		// Skip the detail block once we're inside it.
		if section == "detail" {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}

		// Two common shapes for sample rows:
		//   1) "f++++++++++++ZZZZ : /etc/example"     (3 fields, ":" separator)
		//   2) "f++++++++++++ZZZZ /etc/example"       (2 fields)
		//   3) "added: /etc/example"                  (custom report_url format)
		//   4) "/etc/example"                         (rare, path-only)
		// Pick the path = last absolute-path-looking token.
		var path string
		for i := len(fields) - 1; i >= 0; i-- {
			if strings.HasPrefix(fields[i], "/") {
				path = fields[i]
				break
			}
		}
		if path == "" {
			continue
		}

		// Classify by leading attribute token (most reliable):
		first := fields[0]
		if len(first) >= 2 && strings.ContainsRune("fdlcbDpsSh", rune(first[0])) {
			pushSample(classifyAttr(first), path)
			continue
		}

		// Fallback: section context.
		switch section {
		case "added", "removed", "changed":
			pushSample(section, path)
			continue
		}

		// Last shot: literal "added:/removed:/changed:" prefixes from a
		// custom report_format. Section context already covers most of
		// this but explicit handling is cheap.
		switch {
		case strings.HasPrefix(trimmed, "added: "):
			pushSample("added", path)
		case strings.HasPrefix(trimmed, "removed: "):
			pushSample("removed", path)
		case strings.HasPrefix(trimmed, "changed: "):
			pushSample("changed", path)
		}
	}
	resp.LastCheckTS = lastTimestamp
	_ = section // section accumulates state across iterations; final value not used
}

// mwAideCheckHandler invokes `aide --check` synchronously. Times out
// at the agent's command timeout — operator should rely on the timer
// for full runs.
func mwAideCheckHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	if _, err := os.Stat("/usr/bin/aide"); err != nil {
		return nil, mwInternal("aide binary not found", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := osexec.CommandContext(ctx, "/usr/bin/aide", "--config", "/etc/aide/aide.conf", "--check")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, mwInternal("aide stdout pipe", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, mwInternal("aide start", err)
	}
	out, _ := io.ReadAll(stdout)
	if err := cmd.Wait(); err != nil {
		// AIDE returns non-zero when diffs are found — not an error.
		if ee, ok := err.(*osexec.ExitError); ok && ee.ExitCode() < 8 {
			// fall through — diffs are expected output
		} else {
			return nil, mwInternal("aide wait", err)
		}
	}

	resp := aideStatusResponse{Enabled: true, Sample: []aideSampleRow{}}
	parseAideReport(string(out), &resp)
	if st, err := os.Stat(aideDBPath); err == nil {
		resp.DBAgeSeconds = int64(time.Since(st.ModTime()).Seconds())
	}
	return resp, nil
}

func init() {
	Default.Register("security.aide.status", mwAideStatusHandler)
	Default.Register("security.aide.check", mwAideCheckHandler)
}
