package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestPHPPoolRemoveHandler(t *testing.T) {
	tests := []struct {
		name      string
		input     phpPoolRemoveParams
		wantError bool
		wantCode  string
	}{
		// Valid case
		{
			name: "valid: username correct",
			input: phpPoolRemoveParams{
				Username: "alice",
			},
			wantError: false,
		},

		// Username validation
		{
			name: "invalid: empty username",
			input: phpPoolRemoveParams{
				Username: "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username starts with digit",
			input: phpPoolRemoveParams{
				Username: "1alice",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username contains uppercase",
			input: phpPoolRemoveParams{
				Username: "Alice",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username contains space",
			input: phpPoolRemoveParams{
				Username: "alice bob",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username too long (>32 chars)",
			input: phpPoolRemoveParams{
				Username: "a1234567890123456789012345678901234",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := phpPoolRemoveHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("wantError=%v, got err=%v", tt.wantError, err)
			}

			if tt.wantError && err != nil {
				aerr, ok := err.(*agentwire.AgentError)
				if !ok {
					t.Errorf("expected AgentError, got %T: %v", err, err)
				} else if aerr.Code != tt.wantCode {
					t.Errorf("wantCode=%s, got code=%s: %s", tt.wantCode, aerr.Code, aerr.Message)
				}
			}
		})
	}
}
