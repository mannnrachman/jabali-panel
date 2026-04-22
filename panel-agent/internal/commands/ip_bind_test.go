package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestIPBindHandler_ValidationErrors covers payload validation — these
// paths never shell out, so they're safe to run in CI without NET_ADMIN.
func TestIPBindHandler_ValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantMsg string
	}{
		{"empty payload", `{}`, "address required"},
		{"malformed json", `not-json`, ""},
		{"not parseable", `{"address":"garbage"}`, "not parseable"},
		{"prefixlen too big v4", `{"address":"203.0.113.7","prefixlen":33}`, "IPv4 range"},
		{"prefixlen too big v6", `{"address":"2001:db8::1","prefixlen":129}`, "IPv6 range"},
		{"prefixlen zero v4", `{"address":"203.0.113.7","prefixlen":0}`, "IPv4 range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ipBindHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

// TestIPUnbindHandler_Validation covers the unbind payload rejection
// paths that don't depend on root/NET_ADMIN.
func TestIPUnbindHandler_Validation(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"empty", `{}`},
		{"bad ip", `{"address":"foo"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ipUnbindHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

// TestProbeLocalReachability_Loopback validates the probe against the
// loopback — guaranteed reachable, no firewall complications. This
// exercises the listener + accept + dial code paths without touching
// `ip` at all.
func TestProbeLocalReachability_Loopback(t *testing.T) {
	ok, err := probeLocalReachability("127.0.0.1")
	if err != nil {
		t.Fatalf("probe on loopback failed: %v", err)
	}
	if !ok {
		t.Fatalf("probe on loopback returned reachable=false")
	}
}

