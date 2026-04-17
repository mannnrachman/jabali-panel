package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDBRestoreHandler(t *testing.T) {
	tests := []struct {
		name      string
		input     dbRestoreParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty db_name",
			input: dbRestoreParams{
				DBName: "",
				Path:   "/var/lib/jabali/restore/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: starts with number",
			input: dbRestoreParams{
				DBName: "1alice",
				Path:   "/var/lib/jabali/restore/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: uppercase",
			input: dbRestoreParams{
				DBName: "Alice",
				Path:   "/var/lib/jabali/restore/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains slash",
			input: dbRestoreParams{
				DBName: "alice/wp",
				Path:   "/var/lib/jabali/restore/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains semicolon",
			input: dbRestoreParams{
				DBName: "alice;drop",
				Path:   "/var/lib/jabali/restore/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains space",
			input: dbRestoreParams{
				DBName: "alice wp",
				Path:   "/var/lib/jabali/restore/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path not under /var/lib/jabali/restore/",
			input: dbRestoreParams{
				DBName: "alice",
				Path:   "/tmp/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with directory traversal",
			input: dbRestoreParams{
				DBName: "alice",
				Path:   "/var/lib/jabali/restore/../../etc/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: file not found",
			input: dbRestoreParams{
				DBName: "alice",
				Path:   "/var/lib/jabali/restore/nonexistent.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := dbRestoreHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("dbRestoreHandler: expected error = %v, got %v", tt.wantError, err)
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
