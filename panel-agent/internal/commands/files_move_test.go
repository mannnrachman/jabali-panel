package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestFilesMoveHandler_Validation(t *testing.T) {
	testUser := currentTestUser(t)

	tests := []struct {
		name      string
		input     filesMoveParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty username",
			input: filesMoveParams{
				UserID: "user123", OldPath: "/a.txt", NewPath: "/b/a.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty old path",
			input: filesMoveParams{
				UserID: "user123", Username: testUser.Username, NewPath: "/b/a.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: empty new path",
			input: filesMoveParams{
				UserID: "user123", Username: testUser.Username, OldPath: "/a.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: old path traversal",
			input: filesMoveParams{
				UserID: "user123", Username: testUser.Username,
				OldPath: "../etc/passwd", NewPath: "/b/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: new path traversal",
			input: filesMoveParams{
				UserID: "user123", Username: testUser.Username,
				OldPath: "/a.txt", NewPath: "../etc/shadow",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: new path absolute outside home",
			input: filesMoveParams{
				UserID: "user123", Username: testUser.Username,
				OldPath: "/a.txt", NewPath: "/etc/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: null byte in path",
			input: filesMoveParams{
				UserID: "user123", Username: testUser.Username,
				OldPath: "/a\x00.txt", NewPath: "/b/a.txt",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)
			_, err := filesMoveHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("expected error = %v, got %v", tt.wantError, err)
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

func TestFilesMoveHandler_RefusesSelfDescendant(t *testing.T) {
	testUser := currentTestUser(t)

	// Moving a dir into its own child. isDescendant is pure-string so
	// it can refuse this without needing the dir to exist on disk —
	// the handler short-circuits before touching the filesystem.
	params, _ := json.Marshal(filesMoveParams{
		UserID:   "user123",
		Username: testUser.Username,
		OldPath:  "/work",
		NewPath:  "/work/inside",
	})
	_, err := filesMoveHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error moving dir into its own descendant")
	}
	var ae *agentwire.AgentError
	if !isAgentError(err, &ae) || ae.Code != agentwire.CodeInvalidArgument {
		t.Errorf("wrong error: %v", err)
	}
}

func TestIsDescendant(t *testing.T) {
	cases := []struct {
		ancestor, descendant string
		want                 bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/b/c/d", true},
		{"/a/b", "/a/bar", false}, // prefix-match trap — must NOT match
		{"/a/b", "/a", false},
		{"/a/b", "/x", false},
	}
	for _, tc := range cases {
		if got := isDescendant(tc.ancestor, tc.descendant); got != tc.want {
			t.Errorf("isDescendant(%q, %q) = %v, want %v", tc.ancestor, tc.descendant, got, tc.want)
		}
	}
}
