package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDBDropHandler(t *testing.T) {
	tests := []struct {
		name      string
		input     dbDropParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty db_name",
			input: dbDropParams{
				DBName: "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: starts with number",
			input: dbDropParams{
				DBName: "1alice",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains slash",
			input: dbDropParams{
				DBName: "alice/wp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains semicolon",
			input: dbDropParams{
				DBName: "alice;wp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := dbDropHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("dbDropHandler: expected error = %v, got %v", tt.wantError, err)
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
