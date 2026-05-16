package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/cronvalidate"
)

// TestValidateCronInputs_GateIsWired proves the CLI create/update path
// actually runs the cronvalidate allow-list — the security regression
// M44 closed (cron_cmd.go previously wrote rows with zero validation
// while its help text claimed "allowlisted commands only").
//
// We exercise the name + schedule legs only: both run BEFORE
// ownedDocrootsForUser touches the global sharedDB, so the test needs
// no DB fixture. Command-allow-list depth is covered exhaustively by
// internal/cronvalidate's own fuzz + unit suite; this test's job is
// solely to assert the CLI calls into that gate at all.
func TestValidateCronInputs_GateIsWired(t *testing.T) {
	ctx := context.Background()

	// Control-char in name → cronvalidate.ValidateCronName rejects,
	// before any DB access.
	if err := validateCronInputs(ctx, "u_irrelevant", "alice", "bad\x00name", "*/5 * * * *", "/usr/bin/php -v"); err == nil {
		t.Fatal("expected error for control-char cron name, got nil — validator not wired")
	} else if !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("expected name validation error, got: %v", err)
	}

	// Malformed schedule → cronvalidate.ValidateSchedule rejects,
	// before any DB access.
	if err := validateCronInputs(ctx, "u_irrelevant", "alice", "nightly", "not-a-cron-expr", "/usr/bin/php -v"); err == nil {
		t.Fatal("expected error for malformed schedule, got nil — validator not wired")
	} else if !strings.Contains(err.Error(), "invalid schedule") {
		t.Fatalf("expected schedule validation error, got: %v", err)
	}
}

// Drift regression guard: validateCronInputs (the shared CLI cron gate
// used by `cron add` + `cron update`) MUST reject a user with no Linux
// account, exactly as REST cron.create does (409
// user_has_no_linux_account). Pre-fix the CLI skipped this and created
// an un-materialisable enabled job. Pure inputs only (no DB).
func TestValidateCronInputs_RejectsNoLinuxAccount(t *testing.T) {
	ctx := context.Background()
	// Empty username = no Linux account → must be rejected with the
	// shared cronvalidate sentinel, same rule REST enforces.
	err := validateCronInputs(ctx, "u1", "", "nightly", "*/5 * * * *", "/usr/bin/php -v")
	if err == nil {
		t.Fatal("CLI accepted a cron job for a user with NO Linux account — REST/CLI drift (ADR-0083)")
	}
	if !errors.Is(err, cronvalidate.ErrNoLinuxAccount) {
		t.Fatalf("expected cronvalidate.ErrNoLinuxAccount, got: %v", err)
	}
	// Sanity: a valid Linux username passes the gate (then fails later
	// only if other inputs are bad — here all inputs valid except we
	// don't reach DB because docroots come after; assert it's NOT the
	// no-linux error).
	if err := validateCronInputs(ctx, "u1", "alice", "bad\x00", "*/5 * * * *", "x"); errors.Is(err, cronvalidate.ErrNoLinuxAccount) {
		t.Fatal("valid username wrongly flagged as no-linux-account")
	}
}
