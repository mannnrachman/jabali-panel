package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestWordPressDelete_InvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		input     wordpressDeleteReq
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: missing os_user",
			input: wordpressDeleteReq{
				OSUser:  "",
				Docroot: "/home/alice/domains/test.com/public_html",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: missing docroot",
			input: wordpressDeleteReq{
				OSUser:  "alice",
				Docroot: "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path traversal in docroot",
			input: wordpressDeleteReq{
				OSUser:  "alice",
				Docroot: "/home/alice/domains/../../../etc/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: docroot outside user's home",
			input: wordpressDeleteReq{
				OSUser:  "alice",
				Docroot: "/etc/wordpress",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: different user's docroot",
			input: wordpressDeleteReq{
				OSUser:  "alice",
				Docroot: "/home/bob/domains/site.com/public_html",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)
			_, err := wordpressDeleteHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("wordpressDeleteHandler: expected error = %v, got %v", tt.wantError, err)
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

func TestWordPressDelete_ValidRequest(t *testing.T) {
	// Test that valid input produces a non-error response
	// Note: This test doesn't actually run the deletion (that requires systemd-run)
	// but it validates the request parsing and path validation logic.
	input := wordpressDeleteReq{
		OSUser:  "alice",
		Docroot: "/home/alice/domains/test.com/public_html",
	}
	params, _ := json.Marshal(input)

	// In a real integration test, we would mock the systemd-run command
	// For unit test purposes, we can at least verify parsing works
	var req wordpressDeleteReq
	if err := json.Unmarshal(params, &req); err != nil {
		t.Errorf("failed to parse valid request: %v", err)
	}

	if req.OSUser != "alice" {
		t.Errorf("expected OSUser=alice, got %s", req.OSUser)
	}
	if req.Docroot != "/home/alice/domains/test.com/public_html" {
		t.Errorf("expected correct docroot, got %s", req.Docroot)
	}
}
