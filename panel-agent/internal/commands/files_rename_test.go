package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestFilesRenameHandler(t *testing.T) {
	testUser := currentTestUser(t)

	tests := []struct {
		name      string
		input     filesRenameParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty username",
			input: filesRenameParams{
				UserID:   "user123",
				Username: "",
				OldPath:  "/oldfile.txt",
				NewPath:  "/newfile.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty old path",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "",
				NewPath:  "/newfile.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty new path",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile.txt",
				NewPath:  "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: old path traversal with ..",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "../etc/passwd",
				NewPath:  "/newfile.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: new path traversal with ..",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile.txt",
				NewPath:  "../etc/shadow",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: old path absolute outside home",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/etc/passwd",
				NewPath:  "/newfile.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: new path absolute outside home",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile.txt",
				NewPath:  "/etc/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: old path with null byte",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile\x00.txt",
				NewPath:  "/newfile.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: new path with null byte",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile.txt",
				NewPath:  "/newfile\x00.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: old path with control character",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile\x01.txt",
				NewPath:  "/newfile.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: new path with control character",
			input: filesRenameParams{
				UserID:   "user123",
				Username: testUser.Username,
				OldPath:  "/oldfile.txt",
				NewPath:  "/newfile\x01.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := filesRenameHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("filesRenameHandler: expected error = %v, got %v", tt.wantError, err)
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
