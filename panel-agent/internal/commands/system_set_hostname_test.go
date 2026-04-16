package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// TestSystemSetHostnameHandler_InvalidInput covers the pre-exec
// validation path. We never reach hostnamectl for these inputs, so
// running them in the test harness (no hostnamectl binary, no root) is
// safe. Valid-path exec-side behaviour is covered by integration tests.
func TestSystemSetHostnameHandler_InvalidInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload string
		wantMsg string
	}{
		{"empty", `{"hostname":""}`, "invalid hostname"},
		{"whitespace_only", `{"hostname":"   "}`, "invalid hostname"},
		{"command_injection", `{"hostname":"foo; rm -rf /"}`, "invalid hostname"},
		{"newline", `{"hostname":"foo\nbar"}`, "invalid hostname"},
		{"too_long", `{"hostname":"` + strings.Repeat("a", 254) + `"}`, "invalid hostname"},
		{"leading_dash", `{"hostname":"-foo.com"}`, "invalid hostname"},
		{"trailing_dot_only", `{"hostname":"."}`, "invalid hostname"},
		{"spaces", `{"hostname":"foo bar"}`, "invalid hostname"},
		{"malformed_json", `not json`, "failed to parse params"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := systemSetHostnameHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			ae, ok := err.(*agentwire.AgentError)
			if !ok {
				t.Fatalf("expected *agentwire.AgentError, got %T: %v", err, err)
			}
			if ae.Code != agentwire.CodeInvalidArgument {
				t.Errorf("code = %q, want %q", ae.Code, agentwire.CodeInvalidArgument)
			}
			if !strings.Contains(ae.Message, tc.wantMsg) {
				t.Errorf("message = %q, want contains %q", ae.Message, tc.wantMsg)
			}
		})
	}
}

// TestSystemSetHostnameHandler_Registered confirms the init() side-effect
// actually wired the command into the default registry. Missing
// registration would mean the panel silently fails to apply hostname
// changes in production.
func TestSystemSetHostnameHandler_Registered(t *testing.T) {
	t.Parallel()
	found := false
	for _, name := range Default.Commands() {
		if name == "system.set_hostname" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("system.set_hostname not registered in Default registry")
	}
}
