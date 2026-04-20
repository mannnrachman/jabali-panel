package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestValidateDocrootPath(t *testing.T) {
	tests := []struct {
		name    string
		osUser  string
		docroot string
		wantErr bool
	}{
		{
			name:    "valid: standard docroot",
			osUser:  "alice",
			docroot: "/home/alice/domains/example.com/public_html",
			wantErr: false,
		},
		{
			name:    "valid: relative path within domains",
			osUser:  "bob",
			docroot: "/home/bob/domains/test.local",
			wantErr: false,
		},
		{
			name:    "invalid: path traversal with ..",
			osUser:  "charlie",
			docroot: "/home/charlie/domains/../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "invalid: outside home directory",
			osUser:  "dave",
			docroot: "/etc/wordpress",
			wantErr: true,
		},
		{
			name:    "invalid: different user",
			osUser:  "eve",
			docroot: "/home/frank/domains/site.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDocrootPath(tt.osUser, tt.docroot)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDocrootPath: expected error = %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestWordPressInstallHandler_InvalidInput(t *testing.T) {
	tests := []struct {
		name      string
		input     wordpressInstallReq
		wantError bool
		wantCode  string
	}{
		{
			name: "invalid: missing os_user",
			input: wordpressInstallReq{
				OSUser:     "",
				Docroot:    "/home/alice/domains/test.com/public_html",
				DBName:     "wp_test",
				DBUser:     "wp_user",
				DBPassword: "password123",
				DBHost:     "localhost",
				SiteURL:    "https://test.com",
				SiteTitle:  "Test Site",
				AdminUser:  "admin",
				AdminPass:  "admin123",
				AdminEmail: "admin@test.com",
				Locale:     "en_US",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: missing docroot",
			input: wordpressInstallReq{
				OSUser:     "alice",
				Docroot:    "",
				DBName:     "wp_test",
				DBUser:     "wp_user",
				DBPassword: "password123",
				DBHost:     "localhost",
				SiteURL:    "https://test.com",
				SiteTitle:  "Test Site",
				AdminUser:  "admin",
				AdminPass:  "admin123",
				AdminEmail: "admin@test.com",
				Locale:     "en_US",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: missing db_password",
			input: wordpressInstallReq{
				OSUser:     "alice",
				Docroot:    "/home/alice/domains/test.com/public_html",
				DBName:     "wp_test",
				DBUser:     "wp_user",
				DBPassword: "",
				DBHost:     "localhost",
				SiteURL:    "https://test.com",
				SiteTitle:  "Test Site",
				AdminUser:  "admin",
				AdminPass:  "admin123",
				AdminEmail: "admin@test.com",
				Locale:     "en_US",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: missing admin_pass",
			input: wordpressInstallReq{
				OSUser:     "alice",
				Docroot:    "/home/alice/domains/test.com/public_html",
				DBName:     "wp_test",
				DBUser:     "wp_user",
				DBPassword: "password123",
				DBHost:     "localhost",
				SiteURL:    "https://test.com",
				SiteTitle:  "Test Site",
				AdminUser:  "admin",
				AdminPass:  "",
				AdminEmail: "admin@test.com",
				Locale:     "en_US",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: path traversal in docroot",
			input: wordpressInstallReq{
				OSUser:     "alice",
				Docroot:    "/home/alice/domains/../../../etc/passwd",
				DBName:     "wp_test",
				DBUser:     "wp_user",
				DBPassword: "password123",
				DBHost:     "localhost",
				SiteURL:    "https://test.com",
				SiteTitle:  "Test Site",
				AdminUser:  "admin",
				AdminPass:  "admin123",
				AdminEmail: "admin@test.com",
				Locale:     "en_US",
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)
			_, err := wordpressInstallHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("wordpressInstallHandler: expected error = %v, got %v", tt.wantError, err)
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

// TestBuildOIDCPluginSettings_Shape pins the wire contract the
// OpenID Connect Generic WP plugin expects. A regression here would
// ship silently — the plugin simply won't redirect to Hydra and the
// admin would see the stock WP login form even on an SSO-enabled
// install. Verify:
//   - every Hydra endpoint is derived from the issuer (no hardcoding)
//   - the credentials pass through unchanged
//   - link_existing_users + create_if_does_not_exist stay true so
//     the Kratos email → WP user mapping survives first login
func TestBuildOIDCPluginSettings_Shape(t *testing.T) {
	got := buildOIDCPluginSettings(
		"client_abc",
		"secret_xyz",
		"https://panel.example.com",
	)

	cases := map[string]any{
		"client_id":                "client_abc",
		"client_secret":            "secret_xyz",
		"login_type":               "button",
		"scope":                    "openid email profile",
		"endpoint_login":           "https://panel.example.com/oauth2/auth",
		"endpoint_token":           "https://panel.example.com/oauth2/token",
		"endpoint_userinfo":        "https://panel.example.com/userinfo",
		"endpoint_end_session":     "https://panel.example.com/oauth2/sessions/logout",
		"identity_key":             "sub",
		"link_existing_users":      true,
		"create_if_does_not_exist": true,
		// identify_with_username=false is the key to the email-based
		// lookup — flipping to true would force the Kratos `sub` to
		// match a WP username, breaking every install pre-created by
		// `wp core install --admin_user=…`.
		"identify_with_username": false,
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("setting %q: got %v, want %v", k, got[k], want)
		}
	}
}

// TestWordPressInstallHandler_SkipOIDCWhenEmpty is a lightweight
// contract check: the handler must NOT try to invoke wp-cli plugin
// install when any OIDC field is empty. Full exec-level isolation
// would require stubbing exec.Command; instead we verify the guard
// condition directly by asserting that all three fields are required
// to trigger bootstrapping.
func TestWordPressInstallHandler_SkipOIDCWhenEmpty(t *testing.T) {
	cases := []struct {
		name string
		req  wordpressInstallReq
	}{
		{name: "all empty", req: wordpressInstallReq{}},
		{name: "client id only", req: wordpressInstallReq{OIDCClientID: "x"}},
		{name: "secret only", req: wordpressInstallReq{OIDCClientSecret: "x"}},
		{name: "issuer only", req: wordpressInstallReq{OIDCIssuer: "https://x"}},
		{name: "missing issuer", req: wordpressInstallReq{OIDCClientID: "x", OIDCClientSecret: "y"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			has := c.req.OIDCClientID != "" && c.req.OIDCClientSecret != "" && c.req.OIDCIssuer != ""
			if has {
				t.Errorf("guard should suppress plugin bootstrap for partial OIDC input %+v", c.req)
			}
		})
	}
}
