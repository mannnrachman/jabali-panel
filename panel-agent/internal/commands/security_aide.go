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

// parseAideReport extracts summary + sample paths from an aide.report.log.
// AIDE's text output is stable across versions; we parse the
// "Summary" block + "Detailed information" block.
func parseAideReport(text string, resp *aideStatusResponse) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var lastTimestamp string

	for scanner.Scan() {
		line := scanner.Text()
		// Header form: "Start timestamp: 2026-04-30 04:30:42 +0000 (AIDE 0.18)"
		if strings.HasPrefix(line, "Start timestamp:") {
			lastTimestamp = strings.TrimSpace(strings.TrimPrefix(line, "Start timestamp:"))
		}
		if strings.HasPrefix(line, "  Total number of entries:") {
			continue
		}
		if strings.HasPrefix(line, "  Added entries:") {
			fmt.Sscanf(line, "  Added entries:%d", &resp.Summary.Added)
		}
		if strings.HasPrefix(line, "  Removed entries:") {
			fmt.Sscanf(line, "  Removed entries:%d", &resp.Summary.Removed)
		}
		if strings.HasPrefix(line, "  Changed entries:") {
			fmt.Sscanf(line, "  Changed entries:%d", &resp.Summary.Changed)
		}
		// Sample paths: lines starting with "f " / "d " / "+++" / "---"
		if strings.HasPrefix(line, "added: ") {
			resp.Sample = append(resp.Sample, aideSampleRow{
				Path: strings.TrimSpace(strings.TrimPrefix(line, "added: ")),
				ChangeType: "added",
			})
		}
		if strings.HasPrefix(line, "removed: ") {
			resp.Sample = append(resp.Sample, aideSampleRow{
				Path: strings.TrimSpace(strings.TrimPrefix(line, "removed: ")),
				ChangeType: "removed",
			})
		}
		if strings.HasPrefix(line, "changed: ") {
			resp.Sample = append(resp.Sample, aideSampleRow{
				Path: strings.TrimSpace(strings.TrimPrefix(line, "changed: ")),
				ChangeType: "changed",
			})
		}
		if len(resp.Sample) >= 50 {
			break
		}
	}
	resp.LastCheckTS = lastTimestamp
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
