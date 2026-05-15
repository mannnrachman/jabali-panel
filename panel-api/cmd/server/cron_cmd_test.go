package main

import (
	"context"
	"strings"
	"testing"
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
	if err := validateCronInputs(ctx, "u_irrelevant", "bad\x00name", "*/5 * * * *", "/usr/bin/php -v"); err == nil {
		t.Fatal("expected error for control-char cron name, got nil — validator not wired")
	} else if !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("expected name validation error, got: %v", err)
	}

	// Malformed schedule → cronvalidate.ValidateSchedule rejects,
	// before any DB access.
	if err := validateCronInputs(ctx, "u_irrelevant", "nightly", "not-a-cron-expr", "/usr/bin/php -v"); err == nil {
		t.Fatal("expected error for malformed schedule, got nil — validator not wired")
	} else if !strings.Contains(err.Error(), "invalid schedule") {
		t.Fatalf("expected schedule validation error, got: %v", err)
	}
}
