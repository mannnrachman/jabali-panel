package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDNSZoneDeleteValidation(t *testing.T) {
	tests := []struct {
		name        string
		params      string
		expectedErr bool
		expectedMsg string
	}{
		{
			name:        "missing zone",
			params:      `{}`,
			expectedErr: true,
			expectedMsg: "zone required",
		},
		{
			name:        "empty zone",
			params:      `{"zone": ""}`,
			expectedErr: true,
			expectedMsg: "zone required",
		},
		{
			name:        "malformed json",
			params:      `{invalid json}`,
			expectedErr: true,
			expectedMsg: "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := dnsZoneDeleteHandler(context.Background(), json.RawMessage(tt.params))
			if tt.expectedErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				errMsg := err.Error()
				if !findInString(errMsg, tt.expectedMsg) {
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

func TestDNSZoneDeleteClientUnavailable(t *testing.T) {
	// When pdns client is not available, handler should return friendly error
	params := json.RawMessage(`{"zone": "example.com"}`)

	// pdns.Default() will be nil if ReadEnvAndConnect wasn't called
	resp, err := dnsZoneDeleteHandler(context.Background(), params)

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
	if !findInString(errMsg, "powerdns backend not available") {
		t.Fatalf("expected error about unavailable backend, got %q", errMsg)
	}
}

func TestDNSZoneDeleteCommandRegistered(t *testing.T) {
	commands := Default.Commands()
	found := false
	for _, cmd := range commands {
		if cmd == "dns.zone.delete" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("dns.zone.delete command not registered; available: %v", commands)
	}
}
