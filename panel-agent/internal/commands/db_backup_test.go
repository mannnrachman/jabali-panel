package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDBBackupHandler(t *testing.T) {
	tests := []struct {
		name      string
		input     dbBackupParams
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: empty db_name",
			input: dbBackupParams{
				DBName: "",
				Path:   "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: starts with number",
			input: dbBackupParams{
				DBName: "1alice",
				Path:   "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: starts with dash",
			input: dbBackupParams{
				DBName: "-bad",
				Path:   "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains slash",
			input: dbBackupParams{
				DBName: "alice/wp",
				Path:   "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains semicolon",
			input: dbBackupParams{
				DBName: "alice;drop",
				Path:   "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: contains space",
			input: dbBackupParams{
				DBName: "alice wp",
				Path:   "",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path not under /var/lib/jabali/backups/",
			input: dbBackupParams{
				DBName: "alice",
				Path:   "/tmp/backup.sql",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path with directory traversal",
			input: dbBackupParams{
				DBName: "alice",
				Path:   "/var/lib/jabali/backups/../../etc/passwd",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := dbBackupHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("dbBackupHandler: expected error = %v, got %v", tt.wantError, err)
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
