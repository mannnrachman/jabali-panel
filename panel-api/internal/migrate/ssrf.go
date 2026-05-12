package migrate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ADR-0095 decision 8: SSRF protection for migration outbound dials.
//
// Every dial from internal/migrate/ (discover-accounts, secrets push,
// pull-source) must call ValidateHost first. The returned Dialer rejects
// at connect-time too — defeats DNS-rebinding attacks where a hostname
// resolves to an allowed public IP at validation and to an internal IP
// when Dial actually fires.
//
// Default-deny ranges (always blocked regardless of operator override):
//   - 127.0.0.0/8 loopback
//   - 169.254.0.0/16 link-local + cloud metadata
//   - ::1/128 IPv6 loopback
//   - fe80::/10 IPv6 link-local
//
// Soft-deny ranges (blocked unless AllowPrivate=true):
//   - 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 RFC1918
//   - fc00::/7 IPv6 ULA

// HostValidator captures the AllowPrivate toggle and the resolved set
// of IPs for one hostname. The dialer reuses these to assert peer-IP
// at connect time.
type HostValidator struct {
	Hostname     string
	AllowPrivate bool
	resolved     []net.IP
}

// ValidateHost resolves hostname to A/AAAA records and rejects if any
// resolved IP falls in a blocked range. Returns a validator that must
// be passed to Dialer() for the eventual TCP connection.
func ValidateHost(ctx context.Context, hostname string, allowPrivate bool) (*HostValidator, error) {
	if strings.TrimSpace(hostname) == "" {
		return nil, errors.New("ssrf: empty hostname")
	}
	// Resolve via context-aware resolver so the operator-level dial
	// timeout (typically 5–10s) covers DNS too.
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
	if err != nil {
		return nil, fmt.Errorf("ssrf: resolve %s: %w", hostname, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("ssrf: %s resolved to zero records", hostname)
	}
	for _, ip := range ips {
		if err := checkIP(ip, allowPrivate); err != nil {
			return nil, fmt.Errorf("ssrf: %s → %s: %w", hostname, ip.String(), err)
		}
	}
	return &HostValidator{
		Hostname:     hostname,
		AllowPrivate: allowPrivate,
		resolved:     ips,
	}, nil
}

// Dialer returns a net.Dialer whose Control hook re-checks the peer IP
// the kernel is about to connect to. If a DNS rebinding attack flipped
// the record between ValidateHost() and Dial(), the new IP fails the
// allow-list and the connection is refused before the TCP handshake.
func (v *HostValidator) Dialer() *net.Dialer {
	allowed := make(map[string]struct{}, len(v.resolved))
	for _, ip := range v.resolved {
		allowed[ip.String()] = struct{}{}
	}
	allow := v.AllowPrivate
	return &net.Dialer{
		Control: func(network, address string, _ syscall.RawConn) error {
			// address is "ip:port" — extract the IP.
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ssrf: split %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("ssrf: unparseable peer IP %q", host)
			}
			if _, ok := allowed[ip.String()]; !ok {
				return fmt.Errorf("ssrf: dial peer %s not in resolved set (DNS rebinding suspected)", ip)
			}
			if err := checkIP(ip, allow); err != nil {
				return fmt.Errorf("ssrf: dial-time check %s: %w", ip, err)
			}
			return nil
		},
	}
}

// DialTCP is the safe outbound entrypoint for every migrate/* package.
// Combines ValidateHost (resolve + range check) with Dialer (peer-IP
// re-check at connect time) in one call. Callers pass the operator
// override flag pulled from server_settings.migration_allow_private_hosts.
//
// Returns a net.Conn ready for ssh.NewClientConn or any other higher-
// level handshake. Caller owns the conn lifetime.
func DialTCP(ctx context.Context, host string, port int, allowPrivate bool, timeout time.Duration) (net.Conn, error) {
	v, err := ValidateHost(ctx, host, allowPrivate)
	if err != nil {
		return nil, err
	}
	d := v.Dialer()
	d.Timeout = timeout
	return d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
}

func checkIP(ip net.IP, allowPrivate bool) error {
	if ip == nil {
		return errors.New("nil IP")
	}
	if ip.IsLoopback() {
		return errors.New("loopback rejected")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return errors.New("link-local rejected (cloud metadata blocked)")
	}
	if ip.IsUnspecified() {
		return errors.New("0.0.0.0 / :: rejected")
	}
	if ip.IsMulticast() {
		return errors.New("multicast rejected")
	}
	if !allowPrivate && ip.IsPrivate() {
		return errors.New("private (RFC1918 / ULA) rejected; flip migration_allow_private_hosts to override")
	}
	return nil
}
