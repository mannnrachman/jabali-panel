package stalwartadmin

import (
	"encoding/json"
	"strings"
	"testing"
)

// Verify the per-sender filter match marshals to the exact shape
// Stalwart 1.0 accepts (live-verified via create->get round-trip on
// .150). A drift here means the throttle silently degrades to
// always-fire (the operator THINKS user@example.com is throttled but
// every sender hits the cap).
func TestNewSenderFilterMatch_WireShape(t *testing.T) {
	m := NewSenderFilterMatch("user@example.com")
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	want := `{"match":{"0":{"if":"sender == 'user@example.com'","then":"true"}},"else":"false"}`
	if got != want {
		t.Errorf("match shape mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestNewSenderDomainFilterMatch_WireShape(t *testing.T) {
	m := NewSenderDomainFilterMatch("example.com")
	body, _ := json.Marshal(m)
	got := string(body)
	want := `{"match":{"0":{"if":"sender_domain == 'example.com'","then":"true"}},"else":"false"}`
	if got != want {
		t.Errorf("match shape mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// Filter match constructors embed the input verbatim into the
// expression literal. Test that an embedded quote shows up — callers
// MUST sanitise (admin handler does via scopeRefEmailRe / scopeRefDomainRe).
// This pins the behaviour so a future "let's add escaping in the
// constructor" change doesn't silently neuter the API-layer guard.
func TestNewSenderFilterMatch_EmbedsRawInput(t *testing.T) {
	m := NewSenderFilterMatch("evil' || true || sender == '")
	body, _ := json.Marshal(m)
	if !strings.Contains(string(body), "evil'") {
		t.Error("constructor should NOT escape — caller must sanitise")
	}
}
