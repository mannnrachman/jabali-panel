package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestFilesDeleteHandler(t *testing.T) {
	testUser := currentTestUser(t)

	tests := []struct {
		name      string
		input     filesDeleteParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty username",
			input: filesDeleteParams{
				UserID:   "user123",
				Username: "",
				Path:     "/file.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty path",
			input: filesDeleteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path traversal with ..",
			input: filesDeleteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "../etc/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: absolute path outside home",
			input: filesDeleteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/etc",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with null byte",
			input: filesDeleteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/file\x00.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with control character",
			input: filesDeleteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/file\x01.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := filesDeleteHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("filesDeleteHandler: expected error = %v, got %v", tt.wantError, err)
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
