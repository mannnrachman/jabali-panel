package pdnsrecursor

import (
	"context"
	"fmt"
	"net"
	"time"
)

// netResolverProbe issues an NS query for the given zone against Addr
// via net.Resolver's pure-Go path. NS is used (not SOA) because
// net.Resolver exposes LookupNS in stdlib; miekg/dns isn't in this
// project's dependency set and we don't want to add it for a single
// probe.
//
// Interpretation:
// - nil error: recursor answered authoritatively via the forwarder
//   we just wrote (or was already answering for this zone from cache).
// - any error (NXDOMAIN, SERVFAIL, timeout): forwarder isn't working;
//   Manager.applyChange rolls back.
type netResolverProbe struct {
	Addr    string        // "ip:port" — typically "127.0.0.1:53"
	Timeout time.Duration // per-query; typical 2s
}

func (p *netResolverProbe) ProbeZone(ctx context.Context, zone string) error {
	if p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	dialer := &net.Dialer{Timeout: p.Timeout}
	addr := p.Addr
	if addr == "" {
		addr = "127.0.0.1:53"
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			// Force UDP; the overridden address is always our recursor.
			// pdns-recursor supports TCP too but UDP is faster for an NS probe.
			return dialer.DialContext(ctx, network, addr)
		},
	}
	nss, err := r.LookupNS(ctx, zone)
	if err != nil {
		return fmt.Errorf("LookupNS %s via %s: %w", zone, addr, err)
	}
	if len(nss) == 0 {
		return fmt.Errorf("LookupNS %s returned 0 records", zone)
	}
	return nil
}
