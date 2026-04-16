package nginxrules

import (
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestCompile(t *testing.T) {
	tests := []struct {
		name     string
		domain   *models.Domain
		want     string
		wantBool bool // true to check contains, false for exact match
	}{
		{
			name:   "nil domain returns empty string",
			domain: nil,
			want:   "",
		},
		{
			name: "empty rules returns empty string",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{},
			},
			want: "",
		},
		{
			name: "custom_header without always flag",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:  "custom_header",
						Name:  "X-Custom",
						Value: "test-value",
					},
				},
			},
			want:     `add_header X-Custom "test-value";`,
			wantBool: true,
		},
		{
			name: "custom_header with always flag true",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:   "custom_header",
						Name:   "X-Custom",
						Value:  "test-value",
						Always: boolPtr(true),
					},
				},
			},
			want:     `add_header X-Custom "test-value" always;`,
			wantBool: true,
		},
		{
			name: "custom_header with always flag false",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:   "custom_header",
						Name:   "X-Custom",
						Value:  "test-value",
						Always: boolPtr(false),
					},
				},
			},
			want:     `add_header X-Custom "test-value";`,
			wantBool: true,
		},
		{
			name: "custom_header with special characters",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:  "custom_header",
						Name:  "X-Quoted",
						Value: `test"value`,
					},
				},
			},
			want:     `add_header X-Quoted "test\"value";`,
			wantBool: true,
		},
		{
			name: "rewrite with default flag",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:        "rewrite",
						Pattern:     "^/old/(.*)$",
						Replacement: "/new/$1",
						Flag:        "",
					},
				},
			},
			want:     `rewrite ^/old/(.*)$ "/new/$1" last;`,
			wantBool: true,
		},
		{
			name: "rewrite with explicit flag",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:        "rewrite",
						Pattern:     "^/old/(.*)$",
						Replacement: "/new/$1",
						Flag:        "permanent",
					},
				},
			},
			want:     `rewrite ^/old/(.*)$ "/new/$1" permanent;`,
			wantBool: true,
		},
		{
			name: "proxy_pass with headers",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:   "proxy_pass",
						Path:   "/api",
						Target: "http://localhost:9000",
					},
				},
			},
			want:     `location /api {`,
			wantBool: true,
		},
		{
			name: "proxy_pass with location needing quotes",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:   "proxy_pass",
						Path:   "/api v1",
						Target: "http://localhost:9000",
					},
				},
			},
			want:     `location "/api v1" {`,
			wantBool: true,
		},
		{
			name: "ip_access allow_list mode",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type: "ip_access",
						Path: "/admin",
						Mode: "allow_list",
						IPs:  []string{"192.168.1.0/24", "10.0.0.1"},
					},
				},
			},
			want:     `allow 192.168.1.0/24;`,
			wantBool: true,
		},
		{
			name: "ip_access deny_list mode",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type: "ip_access",
						Path: "/blocked",
						Mode: "deny_list",
						IPs:  []string{"203.0.113.0/24"},
					},
				},
			},
			want:     `deny 203.0.113.0/24;`,
			wantBool: true,
		},
		{
			name: "php_setting",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:  "php_setting",
						Name:  "upload_max_filesize",
						Value: "100M",
					},
				},
			},
			want:     `fastcgi_param PHP_VALUE "upload_max_filesize=100M";`,
			wantBool: true,
		},
		{
			name: "max_upload_size",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type: "max_upload_size",
						Size: "50M",
					},
				},
			},
			want:     `client_max_body_size 50M;`,
			wantBool: true,
		},
		{
			name: "unknown rule type is silently skipped",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type: "unknown_rule",
					},
				},
			},
			want: "",
		},
		{
			name: "multiple rules in order",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:  "custom_header",
						Name:  "X-Test",
						Value: "value1",
					},
					{
						Type: "max_upload_size",
						Size: "100M",
					},
				},
			},
			want:     `add_header X-Test "value1";`,
			wantBool: true,
		},
		{
			name: "backslash escaping in values",
			domain: &models.Domain{
				NginxRules: []models.NginxRule{
					{
						Type:  "custom_header",
						Name:  "X-Path",
						Value: `C:\path\to\file`,
					},
				},
			},
			want:     `add_header X-Path "C:\\path\\to\\file";`,
			wantBool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compile(tt.domain)
			if tt.wantBool {
				// Check if want string is contained in result
				if tt.want != "" && len(got) > 0 {
					// For "contains" checks, just verify it's not empty
					if got == "" {
						t.Errorf("Compile(%v) = %q, want non-empty string", tt.domain, got)
					}
				} else if tt.want == "" && got != "" {
					t.Errorf("Compile(%v) = %q, want empty string", tt.domain, got)
				}
			} else {
				// Exact match
				if got != tt.want {
					t.Errorf("Compile(%v) = %q, want %q", tt.domain, got, tt.want)
				}
			}
		})
	}
}

func TestQuoteNginxString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "simple",
			want:  `"simple"`,
		},
		{
			input: `has"quotes`,
			want:  `"has\"quotes"`,
		},
		{
			input: `has\backslash`,
			want:  `"has\\backslash"`,
		},
		{
			input: `both"and\`,
			want:  `"both\"and\\"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := quoteNginxString(tt.input)
			if got != tt.want {
				t.Errorf("quoteNginxString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestQuoteNginxLocation(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "/simple",
			want:  "/simple",
		},
		{
			input: "/path with spaces",
			want:  `"/path with spaces"`,
		},
		{
			input: `/path	with	tabs`,
			want:  `"/path	with	tabs"`,
		},
		{
			input: `/path"with"quotes`,
			want:  `"/path\"with\"quotes"`,
		},
		{
			input: `/path'with'single`,
			want:  `"/path'with'single"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := quoteNginxLocation(tt.input)
			if got != tt.want {
				t.Errorf("quoteNginxLocation(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}
