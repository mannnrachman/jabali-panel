package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestFilesMkdirHandler(t *testing.T) {
	testUser := currentTestUser(t)

	tests := []struct {
		name      string
		input     filesMkdirParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty username",
			input: filesMkdirParams{
				UserID:   "user123",
				Username: "",
				Path:     "/mydir",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty path",
			input: filesMkdirParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path traversal with ..",
			input: filesMkdirParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "../tmp/evil",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: absolute path outside home",
			input: filesMkdirParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/tmp",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with null byte",
			input: filesMkdirParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/mydir\x00",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with control character",
			input: filesMkdirParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/mydir\x01",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := filesMkdirHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("filesMkdirHandler: expected error = %v, got %v", tt.wantError, err)
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
