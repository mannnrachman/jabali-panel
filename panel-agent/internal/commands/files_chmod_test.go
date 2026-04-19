package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestFilesChmodHandler_Validation(t *testing.T) {
	testUser := currentTestUser(t)

	tests := []struct {
		name      string
		input     filesChmodParams
		wantError bool
	}{
		{
			name:      "empty username",
			input:     filesChmodParams{Path: "/a", Mode: "0644"},
			wantError: true,
		},
		{
			name:      "empty path",
			input:     filesChmodParams{Username: testUser.Username, Mode: "0644"},
			wantError: true,
		},
		{
			name:      "empty mode",
			input:     filesChmodParams{Username: testUser.Username, Path: "/a"},
			wantError: true,
		},
		{
			name:      "bogus mode",
			input:     filesChmodParams{Username: testUser.Username, Path: "/a", Mode: "not-a-number"},
			wantError: true,
		},
		{
			name:      "mode with file-type bits (100644 from stat -c %a)",
			input:     filesChmodParams{Username: testUser.Username, Path: "/a", Mode: "100644"},
			wantError: true,
		},
		{
			name:      "traversal in path",
			input:     filesChmodParams{Username: testUser.Username, Path: "../etc/passwd", Mode: "0644"},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)
			_, err := filesChmodHandler(context.Background(), params)
			if (err != nil) != tt.wantError {
				t.Errorf("expected error=%v, got err=%v", tt.wantError, err)
			}
			if tt.wantError {
				var ae *agentwire.AgentError
				if !isAgentError(err, &ae) {
					t.Errorf("want AgentError, got %T", err)
				}
			}
		})
	}
}

func TestParseChmodMode(t *testing.T) {
	cases := []struct {
		in      string
		wantOct string // %04o form; "" means expect error
	}{
		{"644", "0644"},
		{"0644", "0644"},
		{"0o644", "0644"},
		{"0O755", "0755"},
		{"755", "0755"},
		{"4755", "4755"}, // setuid — allowed
		{"2755", "2755"}, // setgid — allowed
		{"1777", "1777"}, // sticky — allowed
		{"0", "0000"},
		{"100644", ""}, // file-type bits rejected
		{"10000", ""},  // above 07777 rejected
		{"", ""},
		{"abc", ""},
		{"-1", ""},
	}
	for _, tc := range cases {
		got, err := parseChmodMode(tc.in)
		if tc.wantOct == "" {
			if err == nil {
				t.Errorf("parseChmodMode(%q): expected error, got mode %04o", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseChmodMode(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if formatted := fmt.Sprintf("%04o", got); formatted != tc.wantOct {
			t.Errorf("parseChmodMode(%q) = %s, want %s", tc.in, formatted, tc.wantOct)
		}
	}
}
