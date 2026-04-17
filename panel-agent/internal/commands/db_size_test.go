package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDBSizeValidation(t *testing.T) {
	tests := []struct {
		name    string
		dbName  string
		wantErr bool
	}{
		{
			name:    "valid name",
			dbName:  "mydb",
			wantErr: false,
		},
		{
			name:    "valid name with numbers",
			dbName:  "db123",
			wantErr: false,
		},
		{
			name:    "valid name with underscores",
			dbName:  "my_db",
			wantErr: false,
		},
		{
			name:    "valid name with hyphens",
			dbName:  "my-db",
			wantErr: false,
		},
		{
			name:    "invalid name: starts with number",
			dbName:  "1mydb",
			wantErr: true,
		},
		{
			name:    "invalid name: starts with underscore",
			dbName:  "_mydb",
			wantErr: true,
		},
		{
			name:    "invalid name: contains space",
			dbName:  "my db",
			wantErr: true,
		},
		{
			name:    "invalid name: empty",
			dbName:  "",
			wantErr: true,
		},
		{
			name:    "invalid name: too long",
			dbName:  "a" + string(make([]byte, 64)),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := json.Marshal(dbSizeParams{DBName: tt.dbName})
			if err != nil {
				t.Fatalf("failed to marshal params: %v", err)
			}

			_, err = dbSizeHandler(context.Background(), params)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got %v", tt.wantErr, err)
			}
			if err != nil {
				var ae *agentwire.AgentError
				if _, ok := err.(*agentwire.AgentError); !ok {
					t.Errorf("expected AgentError, got %T", err)
				}
				_ = ae
			}
		})
	}
}
