package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDBCreateHandler(t *testing.T) {
	tests := []struct {
		name      string
		input     dbCreateParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty db_name",
			input: dbCreateParams{
				DBName: "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: starts with number",
			input: dbCreateParams{
				DBName: "1alice",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: uppercase",
			input: dbCreateParams{
				DBName: "Alice",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains slash",
			input: dbCreateParams{
				DBName: "alice/wp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains semicolon",
			input: dbCreateParams{
				DBName: "alice;drop",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains newline",
			input: dbCreateParams{
				DBName: "alice\nwp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains space",
			input: dbCreateParams{
				DBName: "alice wp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains dot",
			input: dbCreateParams{
				DBName: "alice.wp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: too long",
			input: dbCreateParams{
				DBName: "a1234567890123456789012345678901234567890123456789012345678901234",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip tests that would require a real MariaDB. This is a smoke test
			// for validation logic only.
			params, _ := json.Marshal(tt.input)

			_, err := dbCreateHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("dbCreateHandler: expected error = %v, got %v", tt.wantError, err)
			}

			if tt.wantError && tt.wantCode != "" {
				var ae *agentwire.AgentError
				if !isAgentError(err, &ae) {
					t.Errorf("expected AgentError, got %T", err)
				} else if ae.Code != tt.wantCode {
					t.Errorf("expected code %q, got %q", tt.wantCode, ae.Code)
				}
			}
		})
	}
}

// isAgentError extracts an *agentwire.AgentError from an error.
func isAgentError(err error, target **agentwire.AgentError) bool {
	if ae, ok := err.(*agentwire.AgentError); ok {
		*target = ae
		return true
	}
	return false
}
