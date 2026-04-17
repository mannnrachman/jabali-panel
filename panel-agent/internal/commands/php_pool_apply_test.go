package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestPHPPoolApplyHandler(t *testing.T) {
	tests := []struct {
		name      string
		input     phpPoolApplyParams
		wantError bool
		wantCode  string
	}{
		// Valid-params happy path is intentionally not unit-tested:
		// it would require the pool template on disk AND real systemctl
		// reload, both of which the plan forbids in validation-only
		// tests. The happy path is covered by the E2E test in step 9.

		// Username validation
		{
			name: "invalid: empty username",
			input: phpPoolApplyParams{
				Username:                  "",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username starts with digit",
			input: phpPoolApplyParams{
				Username:                  "1alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username contains uppercase",
			input: phpPoolApplyParams{
				Username:                  "Alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username contains space",
			input: phpPoolApplyParams{
				Username:                  "alice bob",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username contains backslash",
			input: phpPoolApplyParams{
				Username:                  `alice\bob`,
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username too long (>32 chars)",
			input: phpPoolApplyParams{
				Username:                  "a1234567890123456789012345678901234",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// PHP version validation
		{
			name: "invalid: missing PHP version",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: malformed PHP version",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// PM mode validation
		{
			name: "invalid: bad pm_mode",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "badmode",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// PM max children validation
		{
			name: "invalid: zero pm_max_children",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             0,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// Process idle timeout validation
		{
			name: "invalid: zero process_idle_timeout_seconds",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 0,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// Admin value validation
		{
			name: "invalid: forbidden admin_value directive",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminValues: []KV{
					{Name: "open_basedir", Value: "/home/alice"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: unknown admin_value directive",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminValues: []KV{
					{Name: "unknown_directive", Value: "value"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// Admin flag validation
		{
			name: "invalid: unknown admin_flag directive",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminFlags: []KV{
					{Name: "unknown_flag", Value: "on"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: admin_flag bad value",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminFlags: []KV{
					{Name: "display_errors", Value: "maybe"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := json.Marshal(tt.input)

			_, err := phpPoolApplyHandler(context.Background(), params)

			if (err != nil) != tt.wantError {
				t.Errorf("wantError=%v, got err=%v", tt.wantError, err)
			}

			if tt.wantError && err != nil {
				aerr, ok := err.(*agentwire.AgentError)
				if !ok {
					t.Errorf("expected AgentError, got %T: %v", err, err)
				} else if aerr.Code != tt.wantCode {
					t.Errorf("wantCode=%s, got code=%s: %s", tt.wantCode, aerr.Code, aerr.Message)
				}
			}
		})
	}
}

// TestPoolApplyVersionPin tests the version pin file lifecycle.
func TestPoolApplyVersionPin(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("JABALI_PHP_VER_PIN_ROOT", filepath.Join(tmpDir, "user-phpver"))
	os.Setenv("JABALI_FPM_CONFIG_ROOT", filepath.Join(tmpDir, "fpm"))
	defer func() {
		os.Unsetenv("JABALI_PHP_VER_PIN_ROOT")
		os.Unsetenv("JABALI_FPM_CONFIG_ROOT")
	}()

	// Test readVersionPinFile on non-existent file
	ver, err := readVersionPinFile("testuser")
	if err != nil {
		t.Errorf("readVersionPinFile on non-existent should return empty string, got err: %v", err)
	}
	if ver != "" {
		t.Errorf("expected empty version for non-existent file, got: %s", ver)
	}

	// Test writeVersionPinFile
	if err := writeVersionPinFile("testuser", "8.5"); err != nil {
		t.Fatalf("writeVersionPinFile failed: %v", err)
	}

	// Verify file was written
	pinPath := filepath.Join(tmpDir, "user-phpver", "testuser")
	if _, err := os.Stat(pinPath); err != nil {
		t.Fatalf("version pin file not created: %v", err)
	}

	// Verify content
	ver, err = readVersionPinFile("testuser")
	if err != nil {
		t.Fatalf("readVersionPinFile after write failed: %v", err)
	}
	if ver != "8.5" {
		t.Errorf("expected version 8.5, got: %s", ver)
	}
}

// TestPerUserFPMConfig tests per-user FPM config generation.
func TestPerUserFPMConfig(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("JABALI_FPM_CONFIG_ROOT", filepath.Join(tmpDir, "fpm"))
	defer os.Unsetenv("JABALI_FPM_CONFIG_ROOT")

	if err := writePerUserFPMConfig("testuser", "8.5"); err != nil {
		t.Fatalf("writePerUserFPMConfig failed: %v", err)
	}

	confPath := filepath.Join(tmpDir, "fpm", "testuser.conf")
	if _, err := os.Stat(confPath); err != nil {
		t.Fatalf("FPM config file not created: %v", err)
	}

	content, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "pid = /run/php/jabali-testuser/fpm.pid") {
		t.Errorf("config missing pid directive")
	}
	if !strings.Contains(contentStr, "error_log = /var/log/php-fpm-testuser.log") {
		t.Errorf("config missing error_log directive")
	}
	if !strings.Contains(contentStr, "include=/etc/php/8.5/fpm/pool.d/jabali-testuser.conf") {
		t.Errorf("config missing include directive")
	}
}
