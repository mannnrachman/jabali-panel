package commands

import (
	"strings"
	"testing"
	"time"
)

func TestAggregateBlocklistRaw_CountsAndLatestEnd(t *testing.T) {
	now := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	csv := strings.Join([]string{
		"id,source,ip,reason,action,country,as,events_count,expiration,simulated,alert_id",
		"1,crowdsec,Ip:1.1.1.1,crowdsecurity/http-probing,ban,US,X,4,1h0m0s,false,10",
		"2,crowdsec,Ip:2.2.2.2,crowdsecurity/http-probing,ban,DE,Y,4,3h0m0s,false,11",
		"3,crowdsec,Ip:3.3.3.3,crowdsecurity/ssh-bf,ban,FR,Z,4,30m0s,false,12",
		"", // trailing blank tolerated
	}, "\n")
	agg := map[string]*csBlocklistEntry{}
	aggregateBlocklistRaw("cscli-import", []byte(csv), now, agg)

	hp := agg["cscli-import/crowdsecurity/http-probing"]
	if hp == nil || hp.Count != 2 {
		t.Fatalf("http-probing count = %v, want 2", hp)
	}
	// LatestEnd = now + max(1h,3h) = now+3h
	want := now.Add(3 * time.Hour).UTC().Format(time.RFC3339)
	if hp.LatestEnd != want {
		t.Errorf("LatestEnd = %q, want %q (now+3h)", hp.LatestEnd, want)
	}
	if sb := agg["cscli-import/crowdsecurity/ssh-bf"]; sb == nil || sb.Count != 1 {
		t.Errorf("ssh-bf count = %v, want 1", sb)
	}
}

func TestAggregateBlocklistRaw_SkipsHeaderAndJunk(t *testing.T) {
	now := time.Now().UTC()
	agg := map[string]*csBlocklistEntry{}
	aggregateBlocklistRaw("manual", []byte("id,source,ip,reason,action,country,as,events_count,expiration,simulated,alert_id\n"), now, agg)
	if len(agg) != 0 {
		t.Errorf("header-only input must yield no entries, got %d", len(agg))
	}
	aggregateBlocklistRaw("manual", []byte(""), now, agg)
	aggregateBlocklistRaw("manual", []byte("garbage,not,enough\n"), now, agg)
	if len(agg) != 0 {
		t.Errorf("junk must be skipped, got %d", len(agg))
	}
}

func TestAggregateBlocklistRaw_BadDurationStillCounts(t *testing.T) {
	now := time.Now().UTC()
	agg := map[string]*csBlocklistEntry{}
	csv := "id,source,ip,reason,action,country,as,events_count,expiration,simulated,alert_id\n" +
		"9,lists,Ip:9.9.9.9,crowdsecurity/firehol,ban,US,A,1,not-a-duration,false,1\n"
	aggregateBlocklistRaw("lists", []byte(csv), now, agg)
	e := agg["lists/crowdsecurity/firehol"]
	if e == nil || e.Count != 1 {
		t.Fatalf("must still count a row with unparseable duration: %v", e)
	}
}
