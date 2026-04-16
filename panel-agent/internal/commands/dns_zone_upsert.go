package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdns"
)

type dnsRecordParam struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
	Disabled bool   `json:"disabled"`
}

type dnsZoneUpsertParams struct {
	// Zone is the apex name, no trailing dot. "example.com".
	Zone    string             `json:"zone"`
	Records []dnsRecordParam   `json:"records"`
}

type dnsZoneUpsertResponse struct {
	Zone    string `json:"zone"`
	ZoneID  int64  `json:"zone_id"`
	Records int    `json:"records"`
}

func dnsZoneUpsertHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dnsZoneUpsertParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if p.Zone == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "zone required"}
	}

	// Validate all records before touching the database.
	recs := make([]pdns.Record, 0, len(p.Records))
	for i, r := range p.Records {
		if r.Name == "" {
			return nil, fmt.Errorf("record %d: name required", i)
		}
		if r.Type == "" {
			return nil, fmt.Errorf("record %d: type required", i)
		}
		if r.TTL <= 0 {
			r.TTL = 3600
		}
		recs = append(recs, pdns.Record{
			Name:     r.Name,
			Type:     r.Type,
			Content:  r.Content,
			TTL:      r.TTL,
			Priority: r.Priority,
			Disabled: r.Disabled,
		})
	}

	// Panel is expected to send the full desired record set; we don't
	// reject empty. An empty zone still has its SOA — if panel sent
	// nothing it's likely a bug, but that's panel's problem to catch.
	cl := pdns.Default()
	if cl == nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "powerdns backend not available (install.sh may not have run install_powerdns)",
		}
	}

	zoneID, err := cl.UpsertZone(p.Zone, recs)
	if err != nil {
		return nil, err
	}

	// Signal PowerDNS to reload. NOTIFY is how slave NSes learn of
	// changes; even with no slaves, it's a cheap way to poke pdns.
	// Ignore exit code — if pdns_control isn't reachable we've still
	// committed the SQL change and the next scheduled reload will pick
	// it up.
	_ = exec.CommandContext(ctx, "pdns_control", "notify", p.Zone).Run()

	return dnsZoneUpsertResponse{Zone: p.Zone, ZoneID: zoneID, Records: len(recs)}, nil
}

func init() {
	Default.Register("dns.zone.upsert", dnsZoneUpsertHandler)
}
