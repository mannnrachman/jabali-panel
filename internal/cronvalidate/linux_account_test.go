package cronvalidate

import (
	"errors"
	"testing"
)

// Drift reproducer (ADR-0083 class): REST cron.create rejects a user
// with no Linux account ("user_has_no_linux_account", cron.go:158);
// the CLI `cron add` path (validateCronInputs) never checks this, so
// it creates an ENABLED cron job for a no-Linux user → the reconciler
// stuck-loops forever trying to write a systemd timer for a user that
// doesn't exist. Both paths must share ONE rule. This test pins the
// rule; CLI + REST are then both routed through it.
func TestValidateLinuxAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
		wantErr  bool
	}{
		{"empty rejected", "", true},
		{"whitespace rejected", "   ", true},
		{"valid accepted", "alice", false},
		{"valid with digits", "wp_user1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLinuxAccount(tc.username)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateLinuxAccount(%q) err=%v, wantErr=%v",
					tc.username, err, tc.wantErr)
			}
		})
	}
	// The error must be a recognisable sentinel so REST can map it to
	// 409 user_has_no_linux_account and the CLI to a clear message.
	if err := ValidateLinuxAccount(""); !errors.Is(err, ErrNoLinuxAccount) {
		t.Fatalf("empty username must yield the ErrNoLinuxAccount sentinel, got %v", err)
	}
}
