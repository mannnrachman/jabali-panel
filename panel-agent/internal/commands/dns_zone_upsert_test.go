package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDNSZoneUpsertValidation(t *testing.T) {
	tests := []struct {
		name        string
		params      string
		expectedErr bool
		expectedMsg string
	}{
		{
			name:        "missing zone",
			params:      `{"records": []}`,
			expectedErr: true,
			expectedMsg: "zone required",
		},
		{
			name:        "empty zone",
			params:      `{"zone": "", "records": []}`,
			expectedErr: true,
			expectedMsg: "zone required",
		},
		{
			name:        "malformed json",
			params:      `{invalid json}`,
			expectedErr: true,
			expectedMsg: "invalid character",
		},
		{
			name:        "record missing name",
			params:      `{"zone": "example.com", "records": [{"type": "A", "content": "192.0.2.1"}]}`,
			expectedErr: true,
			expectedMsg: "record 0: name required",
		},
		{
			name:        "record missing type",
			params:      `{"zone": "example.com", "records": [{"name": "example.com", "content": "192.0.2.1"}]}`,
			expectedErr: true,
			expectedMsg: "record 0: type required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := dnsZoneUpsertHandler(context.Background(), json.RawMessage(tt.params))
			if tt.expectedErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				errMsg := err.Error()
				if !contains(errMsg, tt.expectedMsg) {
					t.Fatalf("expected error containing %q, got %q", tt.expectedMsg, errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp == nil {
					t.Fatalf("expected response, got nil")
				}
			}
		})
	}
}

func TestDNSZoneUpsertClientUnavailable(t *testing.T) {
	// When pdns client is not available, handler should return friendly error
	params := json.RawMessage(`{"zone": "example.com", "records": [{"name": "example.com", "type": "A", "content": "192.0.2.1"}]}`)

	// pdns.Default() will be nil if ReadEnvAndConnect wasn't called
	resp, err := dnsZoneUpsertHandler(context.Background(), params)

	if err == nil {
		t.Fatalf("expected error when pdns client unavailable")
	}

	if _, ok := err.(*agentwire.AgentError); !ok {
		t.Fatalf("expected AgentError, got %T", err)
	}

	if resp != nil {
		t.Fatalf("expected nil response on error, got %v", resp)
	}

	errMsg := err.Error()
	if !contains(errMsg, "powerdns backend not available") {
		t.Fatalf("expected error about unavailable backend, got %q", errMsg)
	}
}

func TestDNSZoneUpsertCommandRegistered(t *testing.T) {
	commands := Default.Commands()
	found := false
	for _, cmd := range commands {
		if cmd == "dns.zone.upsert" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("dns.zone.upsert command not registered; available: %v", commands)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || (len(s) > 0 && s[0:len(substr)] == substr) || (len(s) > 0 && len(substr) > 0 && findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
