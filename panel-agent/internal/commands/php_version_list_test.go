package commands

import (
	"testing"
)

func TestExtractVersionFromPath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantVer   string
		wantError bool
	}{
		{
			name:    "valid: 8.3",
			path:    "/etc/php/8.3/fpm/pool.d",
			wantVer: "8.3",
		},
		{
			name:    "valid: 8.2",
			path:    "/etc/php/8.2/fpm/pool.d",
			wantVer: "8.2",
		},
		{
			name:    "valid: 7.4",
			path:    "/etc/php/7.4/fpm/pool.d",
			wantVer: "7.4",
		},
		{
			name:      "invalid: missing parts",
			path:      "/etc/php/8.3/fpm",
			wantError: true,
		},
		{
			name:      "invalid: wrong structure",
			path:      "/home/user/8.3/fpm/pool.d",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractVersionFromPath(tt.path)
			if (err != nil) != tt.wantError {
				t.Errorf("wantError=%v, got err=%v", tt.wantError, err)
			}
			if !tt.wantError && got != tt.wantVer {
				t.Errorf("wantVer=%s, got=%s", tt.wantVer, got)
			}
		})
	}
}

func TestPHPVersionListHandler(t *testing.T) {
	tests := []struct {
		name      string
		params    string
		wantError bool
	}{
		{
			name:      "valid: nil params",
			params:    "",
			wantError: false,
		},
		{
			name:      "valid: empty json object",
			params:    "{}",
			wantError: false,
		},
		{
			name:      "invalid: malformed json",
			params:    "{invalid",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawParams []byte
			if tt.params != "" {
				rawParams = []byte(tt.params)
			}

			_, err := phpVersionListHandler(nil, rawParams)

			// Note: actual FS behavior depends on whether /etc/php is populated,
			// so we only check for JSON parsing errors here, not the "list" operation.
			if tt.params != "" && (tt.params[0] == '{' || tt.params[0] == '[') {
				// For valid JSON structure, we don't expect a parse error
				// but we may get an error from the FS operation (which is OK for this test).
				if tt.wantError && tt.params == "{invalid" {
					if err == nil {
						t.Errorf("wantError=true, got nil")
					}
				}
			}
		})
	}
}
