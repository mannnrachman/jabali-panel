package services

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubResolver struct {
	addrs []string
	err   error
}

func (s stubResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	return s.addrs, s.err
}

func TestPanelCertRoutability_Check(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		hostname   string
		publicIPv4 string
		resolver   stubResolver
		wantRoute  bool
		wantReason string
	}{
		{
			name:       "empty hostname",
			hostname:   "",
			publicIPv4: "203.0.113.5",
			wantReason: "missing hostname",
		},
		{
			name:       "empty public ipv4",
			hostname:   "panel.example.com",
			publicIPv4: "",
			wantReason: "missing public_ipv4",
		},
		{
			name:       "localhost suffix",
			hostname:   "localhost",
			publicIPv4: "203.0.113.5",
			wantReason: "non-routable hostname suffix",
		},
		{
			name:       "dot local suffix",
			hostname:   "panel.local",
			publicIPv4: "203.0.113.5",
			wantReason: "non-routable hostname suffix",
		},
		{
			name:       "dot localdomain suffix",
			hostname:   "panel.localdomain",
			publicIPv4: "203.0.113.5",
			wantReason: "non-routable hostname suffix",
		},
		{
			name:       "dns lookup error surfaces as not routable",
			hostname:   "panel.example.com",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{err: errors.New("nxdomain")},
			wantReason: "dns lookup failed",
		},
		{
			name:       "dns returns no records",
			hostname:   "panel.example.com",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{addrs: nil},
			wantReason: "dns lookup returned no records",
		},
		{
			name:       "dns returns ipv6 only",
			hostname:   "panel.example.com",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{addrs: []string{"2001:db8::1"}},
			wantReason: "dns lookup returned no IPv4 records",
		},
		{
			name:       "dns mismatch surfaces both got and want",
			hostname:   "panel.example.com",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{addrs: []string{"198.51.100.99"}},
			wantReason: "dns points elsewhere (got 198.51.100.99, want 203.0.113.5)",
		},
		{
			name:       "dns match marks routable",
			hostname:   "panel.example.com",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{addrs: []string{"203.0.113.5"}},
			wantRoute:  true,
		},
		{
			name:       "dns match among multiple records",
			hostname:   "panel.example.com",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{addrs: []string{"198.51.100.99", "203.0.113.5"}},
			wantRoute:  true,
		},
		{
			name:       "uppercase hostname normalised",
			hostname:   "PANEL.EXAMPLE.COM",
			publicIPv4: "203.0.113.5",
			resolver:   stubResolver{addrs: []string{"203.0.113.5"}},
			wantRoute:  true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &PanelCertRoutability{Resolver: tc.resolver}
			got, err := p.Check(context.Background(), tc.hostname, tc.publicIPv4)
			if err != nil {
				t.Fatalf("Check returned unexpected error: %v", err)
			}
			if got.Routable != tc.wantRoute {
				t.Fatalf("Routable: got %v, want %v (reason=%q)", got.Routable, tc.wantRoute, got.Reason)
			}
			if !tc.wantRoute && !strings.Contains(got.Reason, tc.wantReason) {
				t.Fatalf("Reason: got %q, want substring %q", got.Reason, tc.wantReason)
			}
		})
	}
}

func TestNewPanelCertRoutability_WiresDefaultResolver(t *testing.T) {
	t.Parallel()
	p := NewPanelCertRoutability()
	if p.Resolver == nil {
		t.Fatalf("constructor must wire a default resolver")
	}
}
