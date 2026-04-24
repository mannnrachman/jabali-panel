package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCSDecisionsAdd_Validation(t *testing.T) {
	cases := []struct {
		payload string
		wantMsg string
	}{
		// Empty + bogus scope
		{`{}`, "scope must be"},
		{`{"scope":"bogus"}`, "scope must be"},
		// scope=ip: bad value
		{`{"scope":"ip","value":"garbage"}`, "valid IP for scope=ip"},
		// scope=range: bad value
		{`{"scope":"range","value":"203.0.113.7"}`, "valid CIDR for scope=range"},
		// scope=country: bad value — auto-uppercases, so only
		// non-2-letter strings reject.
		{`{"scope":"country","value":"XXX"}`, "ISO 3166-1 country code"},
		{`{"scope":"country","value":"1A"}`, "ISO 3166-1 country code"},
		{`{"scope":"country","value":""}`, "ISO 3166-1 country code"},
		// scope=as: bad value
		{`{"scope":"as","value":"NOTANUMBER"}`, "ASN number"},
		// Valid scope+value, bad duration
		{`{"scope":"ip","value":"203.0.113.1"}`, "duration"},
		{`{"scope":"ip","value":"203.0.113.1","duration":"forever"}`, "duration"},
		// Valid scope+value+duration, bad reason
		{`{"scope":"ip","value":"203.0.113.1","duration":"1h","reason":"x"}`, "reason length"},
		{`{"scope":"ip","value":"203.0.113.1","duration":"1h","reason":"` + strings.Repeat("a", 201) + `"}`, "reason length"},
	}
	for _, tc := range cases {
		t.Run(tc.payload[:min(40, len(tc.payload))], func(t *testing.T) {
			_, err := csDecisionsAddHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("got %v, want substring %q", err, tc.wantMsg)
			}
		})
	}
}

func TestCSDecisionsDelete_Validation(t *testing.T) {
	cases := []struct {
		payload string
		wantMsg string
	}{
		{`{}`, "either id or ip"},
		{`{"ip":"bogus"}`, "ip must be"},
	}
	for _, tc := range cases {
		t.Run(tc.payload, func(t *testing.T) {
			_, err := csDecisionsDeleteHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("got %v, want substring %q", err, tc.wantMsg)
			}
		})
	}
}

func TestCSDecisionsList_Validation(t *testing.T) {
	if _, err := csDecisionsListHandler(context.Background(), json.RawMessage(`{"scope":"bogus"}`)); err == nil ||
		!strings.Contains(err.Error(), "scope must be") {
		t.Fatalf("scope validation regressed: %v", err)
	}
	if _, err := csDecisionsListHandler(context.Background(), json.RawMessage(`{"limit":2000}`)); err == nil ||
		!strings.Contains(err.Error(), "limit max") {
		t.Fatalf("limit validation regressed: %v", err)
	}
}

func TestCSHubList_Validation(t *testing.T) {
	if _, err := csHubListHandler(context.Background(), json.RawMessage(`{"type":"bogus"}`)); err == nil ||
		!strings.Contains(err.Error(), "type must be") {
		t.Fatalf("hub.list type validation regressed: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
