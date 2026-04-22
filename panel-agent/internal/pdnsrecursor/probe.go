package pdnsrecursor

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// netResolverProbe verifies the just-written forward is actually being
// honored by the recursor — not merely that "something answered".
//
// The original probe only called LookupNS against the recursor at :53 and
// declared success on any non-empty NS set. That gave a false positive
// whenever the recursor could NOT read recursor.forwards (e.g. chown
// failure → "Permission denied" → forward table empty inside the daemon):
// the recursor fell through to the public internet, returned the zone's
// real-world NS set, and the probe happily accepted it. This is the exact
// shape of the 2026-04-22 production incident.
//
// The fix: query NS for the zone at BOTH the recursor's loopback bind
// (:53) and the authoritative pdns-server's loopback bind (:5300, the
// target of the forward we just installed). If the two NS sets match,
// the recursor is serving the local authoritative answer — the forward
// works. If they differ, the recursor is recursing out and the forward
// is effectively broken, regardless of what the daemon's exit code said.
//
// AuthAddr defaults to 127.0.0.1:5300 (M6.3's split-port layout for
// pdns-server). Callers override in tests.
type netResolverProbe struct {
	// Addr is the recursor's loopback bind ("127.0.0.1:53" typical).
	Addr string
	// AuthAddr is the authoritative server's loopback bind
	// ("127.0.0.1:5300" typical — pdns-server per M6.3 split-port).
	AuthAddr string
	// Timeout is per-query; typical 2s.
	Timeout time.Duration
}

func (p *netResolverProbe) ProbeZone(ctx context.Context, zone string) error {
	if p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}
	recursorAddr := p.Addr
	if recursorAddr == "" {
		recursorAddr = "127.0.0.1:53"
	}
	authAddr := p.AuthAddr
	if authAddr == "" {
		authAddr = "127.0.0.1:5300"
	}

	recursorNS, err := lookupNSAt(ctx, zone, recursorAddr, p.Timeout)
	if err != nil {
		return fmt.Errorf("LookupNS %s via recursor %s: %w", zone, recursorAddr, err)
	}
	authNS, err := lookupNSAt(ctx, zone, authAddr, p.Timeout)
	if err != nil {
		// Auth unreachable is a hard failure — the forward target itself
		// isn't answering, the forward can't possibly work.
		return fmt.Errorf("LookupNS %s via auth %s: %w", zone, authAddr, err)
	}
	if len(authNS) == 0 {
		// Auth returned zero NS records — it doesn't host the zone, so the
		// whole forward premise is broken. Don't let this match a likewise-
		// empty recursor answer and silently "succeed".
		return fmt.Errorf("auth %s returned 0 NS records for %s — zone not provisioned on pdns-server", authAddr, zone)
	}
	if !nsSetsEqual(recursorNS, authNS) {
		return fmt.Errorf("forward verification failed for %s: recursor NS=%v != auth NS=%v (recursor is recursing to public; check recursor.forwards permissions and rec_control reload-zones log)",
			zone, recursorNS, authNS)
	}
	return nil
}

// lookupNSAt resolves NS records for zone at addr (ip:port), returning
// the canonical host list. net.Resolver's custom-Dial path is stdlib-only
// and avoids pulling miekg/dns just for an NS query.
func lookupNSAt(ctx context.Context, zone, addr string, timeout time.Duration) ([]string, error) {
	dialer := &net.Dialer{Timeout: timeout}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}
	nss, err := r.LookupNS(ctx, zone)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(nss))
	for i, ns := range nss {
		out[i] = strings.TrimSuffix(strings.ToLower(ns.Host), ".")
	}
	sort.Strings(out)
	return out, nil
}

// nsSetsEqual compares two already-sorted, lowercased, trailing-dot-
// stripped NS host lists.
func nsSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
