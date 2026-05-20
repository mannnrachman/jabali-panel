package stalwartadmin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Verify the MtaOutboundThrottlePayload marshals to EXACTLY the shape
// .150 spike validated (map keys, ms period, simple match). A drift
// here would be silent in dev and only surface against a live
// Stalwart with `invalidPatch: Invalid value for object property`.
func TestMtaOutboundThrottlePayload_WireShape(t *testing.T) {
	p := MtaOutboundThrottlePayload{
		Description: "spike-out",
		Enable:      true,
		Key:         map[string]bool{ThrottleKeySenderDomain: true},
		Rate:        HourlyRate(100),
		Match:       NewAlwaysFireMatch(),
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	wants := []string{
		`"description":"spike-out"`,
		`"enable":true`,
		`"key":{"senderDomain":true}`,
		`"rate":{"count":100,"period":3600000}`,
		`"match":{"match":{},"else":"true"}`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("payload missing %q\nfull: %s", w, got)
		}
	}
}

func TestHourlyRate_PeriodInMilliseconds(t *testing.T) {
	r := HourlyRate(5)
	if r.Period != 3600*1000 {
		t.Errorf("period = %d, want 3600000 (1h in ms)", r.Period)
	}
	if r.Count != 5 {
		t.Errorf("count = %d, want 5", r.Count)
	}
}

func TestDailyRate_PeriodInMilliseconds(t *testing.T) {
	r := DailyRate(1000)
	if r.Period != 86400*1000 {
		t.Errorf("period = %d, want 86400000 (1d in ms)", r.Period)
	}
}

// Client.Create parses the "Created <Type> <id>" stdout shape stalwart-cli
// emits on success. Pin the parse so an upstream output drift fails
// here, not at runtime.
func TestClient_Create_ParsesAssignedID(t *testing.T) {
	c := NewClient("admin", "secret")
	c.run = func(_ context.Context, args []string) ([]byte, []byte, error) {
		// Spot-check the args: create + json flag + type.
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "create MtaOutboundThrottle --json") {
			t.Errorf("expected `create MtaOutboundThrottle --json` in args, got: %s", joined)
		}
		return []byte("Created MtaOutboundThrottle irxz7ww7abaa\n"), nil, nil
	}
	id, err := c.Create(context.Background(), "MtaOutboundThrottle", map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if id != "irxz7ww7abaa" {
		t.Errorf("id = %q, want %q", id, "irxz7ww7abaa")
	}
}

func TestClient_Create_RejectsUnexpectedStdout(t *testing.T) {
	c := NewClient("admin", "secret")
	c.run = func(context.Context, []string) ([]byte, []byte, error) {
		return []byte("Updated MtaOutboundThrottle x123"), nil, nil // wrong verb
	}
	if _, err := c.Create(context.Background(), "MtaOutboundThrottle", map[string]any{}); err == nil {
		t.Error("expected reject when stdout doesn't start with `Created`")
	}
}

func TestClient_Delete_UsesIdsFlag(t *testing.T) {
	c := NewClient("admin", "secret")
	c.run = func(_ context.Context, args []string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "delete MtaOutboundThrottle --ids irxz7ww7abaa") {
			t.Errorf("expected `delete --ids <id>` form, got: %s", joined)
		}
		return []byte("irxz7ww7abaa deleted\n"), nil, nil
	}
	if err := c.Delete(context.Background(), "MtaOutboundThrottle", "irxz7ww7abaa"); err != nil {
		t.Fatal(err)
	}
}
