package commands

import (
	"context"
	"encoding/json"
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
			name: "invalid: username contains quote",
			input: phpPoolApplyParams{
				Username:                  `alice'bob`,
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
		{
			name: "invalid: username contains dot",
			input: phpPoolApplyParams{
				Username:                  "alice.bob",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: username contains newline",
			input: phpPoolApplyParams{
				Username:                  "alice\n",
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
			name: "invalid: php_version only major",
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
		{
			name: "invalid: php_version only dot",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: php_version leading dot",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                ".3",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: php_version three parts",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.3.0",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: php_version empty",
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

		// PM mode validation
		{
			name: "invalid: pm_mode not in allowed set",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "invalid",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// pm_max_children validation
		{
			name: "invalid: pm_max_children is 0",
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

		// process_idle_timeout_seconds validation
		{
			name: "invalid: process_idle_timeout_seconds is 0",
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

		// admin_values validation
		{
			name: "invalid: disallowed admin_value directive (open_basedir)",
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
			name: "invalid: disallowed admin_value directive (disable_functions)",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminValues: []KV{
					{Name: "disable_functions", Value: "system"},
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
		{
			name: "invalid: admin_value with newline in name",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminValues: []KV{
					{Name: "memory_limit\nopen_basedir", Value: "256M"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},

		// admin_flags validation
		{
			name: "invalid: disallowed admin_flag directive",
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
			name: "invalid: admin_flag value not on or off (uppercase ON)",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminFlags: []KV{
					{Name: "log_errors", Value: "ON"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: admin_flag value not on or off (true)",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminFlags: []KV{
					{Name: "log_errors", Value: "true"},
				},
			},
			wantError: true,
			wantCode:  agentwire.CodeInvalidArgument,
		},
		{
			name: "invalid: admin_flag with newline in name",
			input: phpPoolApplyParams{
				Username:                  "alice",
				PHPVersion:                "8.4",
				PmMode:                    "ondemand",
				PmMaxChildren:             20,
				ProcessIdleTimeoutSeconds: 60,
				AdminFlags: []KV{
					{Name: "log_errors\ndisplay_errors", Value: "on"},
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
