package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestModsecGlobalSet_Validation(t *testing.T) {
	cases := []struct {
		payload string
		wantMsg string
	}{
		{`{}`, "engine_mode must be"},
		{`{"engine_mode":"on"}`, "engine_mode must be"}, // case-sensitive
		{`{"engine_mode":"On","paranoia":0}`, "paranoia must be"},
		{`{"engine_mode":"On","paranoia":5}`, "paranoia must be"},
	}
	for _, tc := range cases {
		t.Run(tc.payload, func(t *testing.T) {
			_, err := modsecGlobalSetHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("got %v, want substring %q", err, tc.wantMsg)
			}
		})
	}
}

func TestModsecAuditTail_Validation(t *testing.T) {
	if _, err := modsecAuditTailHandler(context.Background(), json.RawMessage(`{"lines":-1}`)); err == nil ||
		!strings.Contains(err.Error(), "lines must be") {
		t.Fatalf("negative lines not rejected: %v", err)
	}
	if _, err := modsecAuditTailHandler(context.Background(), json.RawMessage(`{"lines":2000}`)); err == nil ||
		!strings.Contains(err.Error(), "lines must be") {
		t.Fatalf("oversized lines not rejected: %v", err)
	}
}

func TestParseModsecAuditLine_Smoke(t *testing.T) {
	line := `{"transaction":{"time_stamp":"Wed Apr 24 12:00:00 2026","client_ip":"203.0.113.7","request":{"uri":"/wp-admin/admin-ajax.php"},"messages":[{"details":{"ruleId":"942100","severity":"4"}},{"details":{"ruleId":"949110","severity":"2"}}]}}`
	entry := parseModsecAuditLine(line)
	if entry.ParseErr {
		t.Fatalf("expected parse success, got ParseErr=true")
	}
	if entry.Client != "203.0.113.7" {
		t.Errorf("client mismatch: %q", entry.Client)
	}
	if entry.URI != "/wp-admin/admin-ajax.php" {
		t.Errorf("uri mismatch: %q", entry.URI)
	}
	if len(entry.RuleIDs) != 2 {
		t.Errorf("rule_ids count: %d", len(entry.RuleIDs))
	}
}

func TestParseModsecAuditLine_FallbackOnGarbage(t *testing.T) {
	line := `not-json-at-all`
	entry := parseModsecAuditLine(line)
	if !entry.ParseErr {
		t.Errorf("expected ParseErr=true")
	}
	if entry.Raw != line {
		t.Errorf("raw mismatch: %q", entry.Raw)
	}
}
