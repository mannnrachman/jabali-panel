package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// SystemServiceDetailsParams takes a list of unit names. The agent cross-
// checks each against the service-list allowlist before shelling out, so
// a malformed request can't introspect arbitrary system units.
type SystemServiceDetailsParams struct {
	Units []string `json:"units"`
}

// ServiceDetail enriches service.list with metrics M31's server-status
// page wants per row. ActiveEnterTimestamp is the wall-clock when the
// unit last entered the active state; we render uptime from it.
type ServiceDetail struct {
	Unit            string `json:"unit"`
	Active          string `json:"active"`
	Sub             string `json:"sub"`
	LoadState       string `json:"load_state"`
	MemoryBytes     uint64 `json:"memory_bytes"`
	Tasks           int    `json:"tasks"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
	ActiveEnteredAt string `json:"active_entered_at,omitempty"`
}

type SystemServiceDetailsResponse struct {
	Services []ServiceDetail `json:"services"`
}

// systemServiceDetailsHandler shells out to `systemctl show` once per
// caller request, passing every unit at once so we incur a single
// subprocess launch even with 11 services. systemctl prints
// `Property=Value` records separated by blank lines, in the same order
// units were requested.
func systemServiceDetailsHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SystemServiceDetailsParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
		}
	}
	if len(p.Units) == 0 {
		// Default to the panel's own allowlist so the aggregator doesn't
		// have to re-derive it.
		for _, name := range AllowedServices() {
			p.Units = append(p.Units, name+".service")
		}
	}

	// Validate every unit name. systemctl has its own input handling but
	// belt + braces — we only accept what AllowedServices() permits, and
	// a strict character allowlist for any future template-unit forms.
	allowed := map[string]bool{}
	for _, name := range AllowedServices() {
		allowed[name+".service"] = true
	}
	cleaned := make([]string, 0, len(p.Units))
	for _, u := range p.Units {
		if !allowed[u] {
			continue // silently drop non-allowlisted units
		}
		cleaned = append(cleaned, u)
	}
	if len(cleaned) == 0 {
		return SystemServiceDetailsResponse{Services: []ServiceDetail{}}, nil
	}

	args := append([]string{
		"show",
		"-p", "ActiveState",
		"-p", "SubState",
		"-p", "LoadState",
		"-p", "MemoryCurrent",
		"-p", "TasksCurrent",
		"-p", "ActiveEnterTimestamp",
		"-p", "Id",
	}, cleaned...)

	out, err := systemctlRunner(ctx, args...)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("systemctl show: %v: %s", err, out)}
	}

	now := time.Now()
	details := parseSystemctlShow(out, now)
	return SystemServiceDetailsResponse{Services: details}, nil
}

// parseSystemctlShow turns the multi-record `systemctl show` output into
// ServiceDetail rows. Records are separated by a blank line; properties
// are KEY=VALUE one per line. Unset numeric values come back as the
// string "[not set]" — we coerce those to zero rather than poison the
// JSON with NaN.
func parseSystemctlShow(out string, now time.Time) []ServiceDetail {
	var details []ServiceDetail
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		props := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			eq := strings.IndexByte(line, '=')
			if eq < 0 {
				continue
			}
			props[line[:eq]] = line[eq+1:]
		}
		d := ServiceDetail{
			Unit:      props["Id"],
			Active:    props["ActiveState"],
			Sub:       props["SubState"],
			LoadState: props["LoadState"],
		}
		d.MemoryBytes = parseSystemdUint(props["MemoryCurrent"])
		d.Tasks = int(parseSystemdUint(props["TasksCurrent"]))
		if ts := props["ActiveEnterTimestamp"]; ts != "" && ts != "n/a" {
			// Format: "Fri 2026-04-25 10:00:00 UTC". Layout matches
			// systemd's default. Parse failures leave uptime=0; not fatal.
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", ts); err == nil {
				d.ActiveEnteredAt = t.UTC().Format(time.RFC3339)
				if !t.IsZero() && now.After(t) {
					d.UptimeSeconds = int64(now.Sub(t).Seconds())
				}
			}
		}
		details = append(details, d)
	}
	return details
}

// parseSystemdUint coerces systemd's "[not set]"-or-number strings to
// uint64. Empty + sentinel + parse-failure all collapse to 0 which the
// UI renders as "—" or hides.
func parseSystemdUint(s string) uint64 {
	if s == "" || s == "[not set]" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func init() {
	Default.Register("system.service_details", systemServiceDetailsHandler)
}
