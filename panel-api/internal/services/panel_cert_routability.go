// Package services hosts thin orchestration helpers that don't fit a
// single repository or HTTP handler. The panel cert routability check
// lives here so the REST handler and the reconciler can share one
// implementation without dragging GORM into either.
package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// RoutabilityResult tells the caller why the panel hostname does (or
// does not) qualify for a Let's Encrypt issuance attempt.
type RoutabilityResult struct {
	Routable bool
	// Reason is a short, UI-displayable string. Empty when Routable=true.
	Reason string
}

// Resolver is the single dependency PanelCertRoutability has on the
// outside world. Production wiring uses net.DefaultResolver; tests
// inject a stub that returns canned answers without DNS round-trips.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// PanelCertRoutability decides whether a panel hostname is publicly
// routable enough to attempt LE HTTP-01. It deliberately does NOT
// probe port 80 from the panel-api side: a successful TCP probe from
// the panel host doesn't prove that the LE validation server (which
// connects from outside) can reach :80, and a failed probe can't
// distinguish "really blocked" from "lo only" weirdness on multi-NIC
// VPS images. Port-80 reachability is left to certbot, which
// surfaces the real LE error in last_error if validation fails.
type PanelCertRoutability struct {
	Resolver Resolver
}

// NewPanelCertRoutability wires the production resolver
// (net.DefaultResolver). Pass a stubbed Resolver for tests.
func NewPanelCertRoutability() *PanelCertRoutability {
	return &PanelCertRoutability{Resolver: net.DefaultResolver}
}

// Check runs the gate. hostname is the panel's canonical FQDN
// (server_settings.hostname); publicIPv4 is the VPS's outward-facing
// address (server_settings.public_ipv4). Both come from the same row
// the admin manages on the Settings page.
//
// Skip rules, in order:
//
//  1. empty hostname / empty public_ipv4 → not routable; reason
//     "missing hostname" or "missing public_ipv4".
//  2. hostname matches localhost / *.local / *.localdomain → not
//     routable; reason "non-routable hostname suffix". Browsers and
//     LE both refuse these.
//  3. DNS A lookup returns nothing or fails → not routable; reason
//     "dns lookup failed: …".
//  4. None of the resolved addresses match publicIPv4 → not routable;
//     reason "dns points elsewhere (got X.X.X.X, want Y.Y.Y.Y)".
//
// Otherwise routable.
func (p *PanelCertRoutability) Check(ctx context.Context, hostname, publicIPv4 string) (RoutabilityResult, error) {
	host := strings.TrimSpace(strings.ToLower(hostname))
	if host == "" {
		return RoutabilityResult{Routable: false, Reason: "missing hostname"}, nil
	}
	if publicIPv4 == "" {
		return RoutabilityResult{Routable: false, Reason: "missing public_ipv4"}, nil
	}

	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localdomain") {
		return RoutabilityResult{Routable: false, Reason: "non-routable hostname suffix"}, nil
	}

	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	addrs, err := resolver.LookupHost(ctx, host)
	if err != nil {
		return RoutabilityResult{Routable: false, Reason: fmt.Sprintf("dns lookup failed: %v", err)}, nil
	}
	if len(addrs) == 0 {
		return RoutabilityResult{Routable: false, Reason: "dns lookup returned no records"}, nil
	}

	// Walk the response set. We only consider IPv4 matches for the
	// panel-cert routability gate; an AAAA-only result on a panel
	// reachable via IPv6 still falls back to self-signed for now.
	// IPv6-first deployments can override via the staging toggle in
	// M32.1.
	want := strings.TrimSpace(publicIPv4)
	var ipv4Got []string
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		if ip.To4() == nil {
			continue
		}
		ipv4Got = append(ipv4Got, ip.String())
		if ip.String() == want {
			return RoutabilityResult{Routable: true}, nil
		}
	}

	if len(ipv4Got) == 0 {
		return RoutabilityResult{Routable: false, Reason: "dns lookup returned no IPv4 records"}, nil
	}
	return RoutabilityResult{
		Routable: false,
		Reason:   fmt.Sprintf("dns points elsewhere (got %s, want %s)", strings.Join(ipv4Got, ","), want),
	}, nil
}

// ErrNoResolver is returned by helpers that need a resolver and got
// nil. Currently only used by tests to assert the constructor wires
// a sane default.
var ErrNoResolver = errors.New("nil Resolver")
