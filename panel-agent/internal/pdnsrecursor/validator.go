package pdnsrecursor

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Zone regex: RFC1035-ish LDH labels, lowercase, no trailing dot, no
// single-char labels at start (an all-single-char like "a" is OK; "a.b"
// is OK; the anchor guards against leading dot / leading dash).
var zoneRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// validateZone covers the zone-name-only path (RemoveZone).
func validateZone(zone string) error {
	if zone == "" {
		return fmt.Errorf("zone is empty")
	}
	if strings.HasSuffix(zone, ".") {
		return fmt.Errorf("zone %q has trailing dot", zone)
	}
	if zone != strings.ToLower(zone) {
		return fmt.Errorf("zone %q has uppercase characters", zone)
	}
	if !zoneRe.MatchString(zone) {
		return fmt.Errorf("zone %q is not a valid DNS name (RFC1035 LDH labels)", zone)
	}
	// Reject IP-ish zones — those are forwarder targets, not zone names.
	if net.ParseIP(zone) != nil {
		return fmt.Errorf("zone %q looks like an IP address", zone)
	}
	return nil
}

// validateEntry covers zone + addr + port (AddZone).
func validateEntry(e Entry) error {
	if err := validateZone(e.Zone); err != nil {
		return err
	}
	if e.Addr == "" {
		return fmt.Errorf("addr is empty")
	}
	ip := net.ParseIP(e.Addr)
	if ip == nil {
		return fmt.Errorf("addr %q is not a valid IP literal", e.Addr)
	}
	if e.Port <= 0 || e.Port > 65535 {
		return fmt.Errorf("port %d out of range (1-65535)", e.Port)
	}
	// Self-loop guard: reject forwarder addr/port pointing at the
	// recursor's own loopback bind (127.0.0.1:53 / [::1]:53). Any
	// such forwarder would loop a query back into the recursor and
	// burn a thread per hop. Only port 5300 (pdns-server loopback)
	// is a legal forwarder target in the jabali deployment.
	//
	// Note: this is stricter than strictly necessary — port 53 on any
	// non-loopback IP would NOT loop (it hits a public resolver).
	// We enforce loopback-only below, so the "port 53 to loopback"
	// check is what catches the real footgun.
	if ip.IsLoopback() && e.Port == 53 {
		return fmt.Errorf("forwarder %s:%d points at the recursor's own bind — self-loop", e.Addr, e.Port)
	}
	// Forwarder must be loopback. External forwarder targets would
	// turn the recursor into a proxy for arbitrary third-party
	// upstreams — out of scope + breaks the panel-authoritative
	// guarantee the forwarder provides.
	if !ip.IsLoopback() {
		return fmt.Errorf("forwarder %s is not loopback — only loopback targets allowed", e.Addr)
	}
	return nil
}

// parseLine takes a trimmed, non-comment recursor.forwards line and
// returns its Entry.
//
// Wire format: "<zone>=<addr>(:port)?" where addr can be bracketed
// IPv6 "[::1]" or bare IPv4. IPv4 without explicit port infers port
// 5300 (the jabali convention — pdns-server loopback bind).
func parseLine(line string) (Entry, error) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return Entry{}, fmt.Errorf("missing '=' separator: %q", line)
	}
	zone := line[:eq]
	rest := line[eq+1:]

	var addr string
	var portStr string
	switch {
	case strings.HasPrefix(rest, "["):
		// bracketed IPv6: "[addr](:port)?"
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return Entry{}, fmt.Errorf("unterminated '[' in %q", rest)
		}
		addr = rest[1:end]
		tail := rest[end+1:]
		if tail != "" {
			if !strings.HasPrefix(tail, ":") {
				return Entry{}, fmt.Errorf("expected ':port' after ']' in %q", rest)
			}
			portStr = tail[1:]
		}
	case strings.Count(rest, ":") == 1:
		// bare "addr:port" — IPv4 only
		colon := strings.IndexByte(rest, ':')
		addr = rest[:colon]
		portStr = rest[colon+1:]
	case strings.Count(rest, ":") == 0:
		// bare addr, no port
		addr = rest
	default:
		// multiple colons without brackets → bare IPv6, no port
		addr = rest
	}

	port := 5300 // jabali default
	if portStr != "" {
		n, err := strconv.Atoi(portStr)
		if err != nil {
			return Entry{}, fmt.Errorf("port %q is not a number", portStr)
		}
		port = n
	}

	e := Entry{Zone: zone, Addr: addr, Port: port}
	if err := validateEntry(e); err != nil {
		return Entry{}, err
	}
	return e, nil
}
