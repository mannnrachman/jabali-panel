package main

import (
	"reflect"
	"testing"
)

// computeOrphans is the pure-logic core of `jabali domain
// prune-orphans`. Bug here = either false positives (panel deletes
// real domains) or false negatives (orphans linger). Worth a
// thorough matrix.

func TestComputeOrphans_NoSites(t *testing.T) {
	got := computeOrphans(nil, map[string]bool{"foo.com": true})
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestComputeOrphans_AllKnown(t *testing.T) {
	got := computeOrphans(
		[]string{"foo.com", "bar.com"},
		map[string]bool{"foo.com": true, "bar.com": true},
	)
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestComputeOrphans_OneOrphan(t *testing.T) {
	got := computeOrphans(
		[]string{"foo.com", "stale.example"},
		map[string]bool{"foo.com": true},
	)
	want := []string{"stale.example"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputeOrphans_SkipsSystemSites(t *testing.T) {
	got := computeOrphans(
		[]string{
			"default", "default-ssl", "000-default", "000-default-ssl",
			"jabali-panel", "jabali-panel-ssl",
			"jabali-pma", "jabali-adminer", "jabali-webmail",
			"actual-orphan.com",
		},
		map[string]bool{}, // no DB rows
	)
	want := []string{"actual-orphan.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputeOrphans_StripsMailSuffix(t *testing.T) {
	// foo.com-mail is the M6 webmail vhost for foo.com — when foo.com
	// is in the DB, foo.com-mail is NOT an orphan even though no
	// "foo.com-mail" row exists.
	got := computeOrphans(
		[]string{"foo.com", "foo.com-mail"},
		map[string]bool{"foo.com": true},
	)
	if len(got) != 0 {
		t.Errorf("known-domain mail vhost must not be orphaned: %v", got)
	}
}

func TestComputeOrphans_OrphanedMailVhost(t *testing.T) {
	// foo.com-mail with NO foo.com in DB → orphan.
	got := computeOrphans(
		[]string{"foo.com-mail"},
		map[string]bool{},
	)
	want := []string{"foo.com-mail"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputeOrphans_StableOrder(t *testing.T) {
	// Output must be sorted ascending so JSON + tabwriter render
	// deterministically across runs (snapshot tests + operator
	// readability).
	got := computeOrphans(
		[]string{"zebra.com", "apple.com", "mango.com"},
		map[string]bool{},
	)
	want := []string{"apple.com", "mango.com", "zebra.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestComputeOrphans_KnownDomainNotInAgent(t *testing.T) {
	// DB knows about foo.com but agent doesn't have a vhost (perhaps
	// reconciler hasn't created it yet). NOT an orphan in either
	// direction — orphan = on agent, not in DB.
	got := computeOrphans(
		[]string{}, // agent empty
		map[string]bool{"foo.com": true},
	)
	if len(got) != 0 {
		t.Errorf("agent-side empty must produce no orphans; got %v", got)
	}
}
