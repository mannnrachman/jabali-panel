package commands

import (
	"strings"
	"testing"
)

func TestApplyManagedBlock_PreservesOperatorKeys(t *testing.T) {
	operator := "ssh-ed25519 AAAAOPERATORKEY admin@laptop\n"
	got := applyManagedBlock(operator, []string{"ssh-ed25519 AAAAJABALI1 u@host"})
	if !strings.Contains(got, "AAAAOPERATORKEY") {
		t.Fatalf("operator key wiped — THE BUG. got:\n%s", got)
	}
	if !strings.Contains(got, "AAAAJABALI1") {
		t.Errorf("jabali key missing:\n%s", got)
	}
	if !strings.Contains(got, akBeginMarker) || !strings.Contains(got, akEndMarker) {
		t.Errorf("markers missing:\n%s", got)
	}
}

func TestApplyManagedBlock_Idempotent(t *testing.T) {
	op := "ssh-rsa AAAAOP operator\n"
	once := applyManagedBlock(op, []string{"ssh-ed25519 AAAAJ1 a"})
	twice := applyManagedBlock(once, []string{"ssh-ed25519 AAAAJ1 a"})
	if once != twice {
		t.Errorf("not idempotent:\n--once--\n%s\n--twice--\n%s", once, twice)
	}
	// operator key survives repeated passes (the lockout scenario)
	if !strings.Contains(twice, "AAAAOP") {
		t.Errorf("operator key lost after 2 passes:\n%s", twice)
	}
}

func TestApplyManagedBlock_EmptyKeysKeepsOperator(t *testing.T) {
	op := "ssh-ed25519 AAAAOP admin\n"
	got := applyManagedBlock(op, []string{})
	if !strings.Contains(got, "AAAAOP") {
		t.Fatalf("operator key wiped on empty jabali keys — THE BUG:\n%s", got)
	}
	if strings.Contains(got, akBeginMarker) {
		t.Errorf("no jabali keys → no managed block expected:\n%s", got)
	}
}

func TestStripManagedBlock_RemovesOnlyJabaliBlock(t *testing.T) {
	in := "ssh-ed25519 OPKEY admin\n" +
		akBeginMarker + "\nssh-ed25519 JKEY u\n" + akEndMarker + "\n"
	out := stripManagedBlock(in)
	if !strings.Contains(out, "OPKEY") {
		t.Errorf("operator key removed by strip:\n%s", out)
	}
	if strings.Contains(out, "JKEY") || strings.Contains(out, akBeginMarker) {
		t.Errorf("jabali block not stripped:\n%s", out)
	}
}

func TestApplyManagedBlock_NoExisting(t *testing.T) {
	got := applyManagedBlock("", []string{"ssh-ed25519 AAAAJ only"})
	if !strings.Contains(got, "AAAAJ") || !strings.Contains(got, akBeginMarker) {
		t.Errorf("fresh file should contain managed block:\n%s", got)
	}
}
