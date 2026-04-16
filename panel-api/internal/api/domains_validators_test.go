package api

import (
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestValidateNginxRules(t *testing.T) {
	tests := []struct {
		name      string
		rules     models.NginxRules
		wantError bool
		errorMsg  string
	}{
		{
			name:      "empty rules list is valid",
			rules:     []models.NginxRule{},
			wantError: false,
		},
		{
			name: "custom_header with all required fields",
			rules: []models.NginxRule{
				{
					Type:  "custom_header",
					Name:  "X-Custom",
					Value: "test-value",
				},
			},
			wantError: false,
		},
		{
			name: "custom_header missing name",
			rules: []models.NginxRule{
				{
					Type:  "custom_header",
					Value: "test-value",
				},
			},
			wantError: true,
			errorMsg:  "name",
		},
		{
			name: "custom_header missing value",
			rules: []models.NginxRule{
				{
					Type: "custom_header",
					Name: "X-Custom",
				},
			},
			wantError: true,
			errorMsg:  "value",
		},
		{
			name: "rewrite with all required fields",
			rules: []models.NginxRule{
				{
					Type:        "rewrite",
					Pattern:     "^/old/(.*)$",
					Replacement: "/new/$1",
				},
			},
			wantError: false,
		},
		{
			name: "rewrite missing pattern",
			rules: []models.NginxRule{
				{
					Type:        "rewrite",
					Replacement: "/new/$1",
				},
			},
			wantError: true,
			errorMsg:  "pattern",
		},
		{
			name: "rewrite missing replacement",
			rules: []models.NginxRule{
				{
					Type:    "rewrite",
					Pattern: "^/old/(.*)$",
				},
			},
			wantError: true,
			errorMsg:  "replacement",
		},
		{
			name: "rewrite with invalid flag",
			rules: []models.NginxRule{
				{
					Type:        "rewrite",
					Pattern:     "^/old/(.*)$",
					Replacement: "/new/$1",
					Flag:        "invalid",
				},
			},
			wantError: true,
			errorMsg:  "flag",
		},
		{
			name: "rewrite with valid flags",
			rules: []models.NginxRule{
				{
					Type:        "rewrite",
					Pattern:     "^/old/(.*)$",
					Replacement: "/new/$1",
					Flag:        "last",
				},
				{
					Type:        "rewrite",
					Pattern:     "^/a/(.*)$",
					Replacement: "/b/$1",
					Flag:        "break",
				},
				{
					Type:        "rewrite",
					Pattern:     "^/c/(.*)$",
					Replacement: "/d/$1",
					Flag:        "redirect",
				},
				{
					Type:        "rewrite",
					Pattern:     "^/e/(.*)$",
					Replacement: "/f/$1",
					Flag:        "permanent",
				},
			},
			wantError: false,
		},
		{
			name: "proxy_pass with all required fields",
			rules: []models.NginxRule{
				{
					Type:   "proxy_pass",
					Path:   "/api",
					Target: "http://localhost:9000",
				},
			},
			wantError: false,
		},
		{
			name: "proxy_pass with https target",
			rules: []models.NginxRule{
				{
					Type:   "proxy_pass",
					Path:   "/api",
					Target: "https://api.example.com",
				},
			},
			wantError: false,
		},
		{
			name: "proxy_pass missing path",
			rules: []models.NginxRule{
				{
					Type:   "proxy_pass",
					Target: "http://localhost:9000",
				},
			},
			wantError: true,
			errorMsg:  "path",
		},
		{
			name: "proxy_pass missing target",
			rules: []models.NginxRule{
				{
					Type: "proxy_pass",
					Path: "/api",
				},
			},
			wantError: true,
			errorMsg:  "target",
		},
		{
			name: "proxy_pass with non-http target",
			rules: []models.NginxRule{
				{
					Type:   "proxy_pass",
					Path:   "/api",
					Target: "ftp://example.com",
				},
			},
			wantError: true,
			errorMsg:  "target",
		},
		{
			name: "ip_access allow_list with ips",
			rules: []models.NginxRule{
				{
					Type: "ip_access",
					Path: "/admin",
					Mode: "allow_list",
					IPs:  []string{"192.168.1.0/24", "10.0.0.1"},
				},
			},
			wantError: false,
		},
		{
			name: "ip_access deny_list with ips",
			rules: []models.NginxRule{
				{
					Type: "ip_access",
					Path: "/blocked",
					Mode: "deny_list",
					IPs:  []string{"203.0.113.0/24"},
				},
			},
			wantError: false,
		},
		{
			name: "ip_access missing path",
			rules: []models.NginxRule{
				{
					Type: "ip_access",
					Mode: "allow_list",
					IPs:  []string{"192.168.1.0/24"},
				},
			},
			wantError: true,
			errorMsg:  "path",
		},
		{
			name: "ip_access missing IPs",
			rules: []models.NginxRule{
				{
					Type: "ip_access",
					Path: "/admin",
					Mode: "allow_list",
				},
			},
			wantError: true,
			errorMsg:  "ips",
		},
		{
			name: "ip_access invalid mode",
			rules: []models.NginxRule{
				{
					Type: "ip_access",
					Path: "/admin",
					Mode: "invalid_mode",
					IPs:  []string{"192.168.1.0/24"},
				},
			},
			wantError: true,
			errorMsg:  "mode",
		},
		{
			name: "php_setting with name and value",
			rules: []models.NginxRule{
				{
					Type:  "php_setting",
					Name:  "upload_max_filesize",
					Value: "100M",
				},
			},
			wantError: false,
		},
		{
			name: "php_setting missing name",
			rules: []models.NginxRule{
				{
					Type:  "php_setting",
					Value: "100M",
				},
			},
			wantError: true,
			errorMsg:  "name",
		},
		{
			name: "php_setting missing value",
			rules: []models.NginxRule{
				{
					Type: "php_setting",
					Name: "upload_max_filesize",
				},
			},
			wantError: true,
			errorMsg:  "value",
		},
		{
			name: "max_upload_size with size",
			rules: []models.NginxRule{
				{
					Type: "max_upload_size",
					Size: "100M",
				},
			},
			wantError: false,
		},
		{
			name: "max_upload_size missing size",
			rules: []models.NginxRule{
				{
					Type: "max_upload_size",
				},
			},
			wantError: true,
			errorMsg:  "size",
		},
		{
			name: "invalid rule type",
			rules: []models.NginxRule{
				{
					Type: "invalid_type",
				},
			},
			wantError: true,
			errorMsg:  "type",
		},
		{
			name: "control character in custom_header value",
			rules: []models.NginxRule{
				{
					Type:  "custom_header",
					Name:  "X-Test",
					Value: "bad\x00value",
				},
			},
			wantError: true,
			errorMsg:  "control",
		},
		{
			name: "control character in rewrite pattern",
			rules: []models.NginxRule{
				{
					Type:        "rewrite",
					Pattern:     "^/bad\x00$",
					Replacement: "/good",
				},
			},
			wantError: true,
			errorMsg:  "control",
		},
		{
			name: "control character in proxy_pass target",
			rules: []models.NginxRule{
				{
					Type:   "proxy_pass",
					Path:   "/api",
					Target: "http://bad\x00.com",
				},
			},
			wantError: true,
			errorMsg:  "control",
		},
		{
			name: "many rules up to limit",
			rules: func() []models.NginxRule {
				var rules []models.NginxRule
				for i := 0; i < 50; i++ {
					rules = append(rules, models.NginxRule{
						Type: "max_upload_size",
						Size: "100M",
					})
				}
				return rules
			}(),
			wantError: false,
		},
		{
			name: "too many rules exceeds limit",
			rules: func() []models.NginxRule {
				var rules []models.NginxRule
				for i := 0; i < 51; i++ {
					rules = append(rules, models.NginxRule{
						Type: "max_upload_size",
						Size: "100M",
					})
				}
				return rules
			}(),
			wantError: true,
			errorMsg:  "50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNginxRules(tt.rules)
			if (err != nil) != tt.wantError {
				t.Errorf("validateNginxRules() error = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantError && err != nil && tt.errorMsg != "" {
				if err.Error() == "" {
					t.Errorf("validateNginxRules() error message is empty, wanted substring %q", tt.errorMsg)
				}
			}
		})
	}
}

func TestIsValidNginxRuleType(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"custom_header", true},
		{"rewrite", true},
		{"proxy_pass", true},
		{"ip_access", true},
		{"php_setting", true},
		{"max_upload_size", true},
		{"unknown", false},
		{"", false},
		{"CUSTOM_HEADER", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isValidNginxRuleType(tt.input)
			if got != tt.want {
				t.Errorf("isValidNginxRuleType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
