package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestFilesWriteHandler(t *testing.T) {
	testUser := currentTestUser(t)

	// Generate large content string for testing the 100MB cap
	largeContent := strings.Repeat("x", 101*1024*1024)

	tests := []struct {
		name      string
		input     filesWriteParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty username",
			input: filesWriteParams{
				UserID:   "user123",
				Username: "",
				Path:     "/file.txt",
				Content:  "test",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty path",
			input: filesWriteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "",
				Content:  "test",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: content exceeds 100MB",
			input: filesWriteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/file.txt",
				Content:  largeContent,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path traversal with ..",
			input: filesWriteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "../etc/passwd",
				Content:  "test",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: absolute path outside home",
			input: filesWriteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/etc/passwd",
				Content:  "test",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with null byte",
			input: filesWriteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/file\x00.txt",
				Content:  "test",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with control character",
			input: filesWriteParams{
				UserID:   "user123",
				Username: testUser.Username,
				Path:     "/file\x01.txt",
				Content:  "test",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := filesWriteHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("filesWriteHandler: expected error = %v, got %v", tt.wantError, err)
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
