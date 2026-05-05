package kratosclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// M25 Step 2 — Unix-socket transport plumbing.
//
// Kratos's admin (and later public) endpoint binds a Unix-domain socket at
// /run/jabali-kratos/admin.sock with mode 0660 jabali:jabali-sockets. The
// panel-api process runs as jabali (already in jabali-sockets) so it can
// connect, but Go's net/http needs a custom DialContext to actually open
// the socket — the URL becomes synthetic ("http://kratos-admin/...") and
// the dialer routes by hostname to the right socket path.
//
// Why the synthetic-host indirection: the existing client packs URLs as
// `c.adminURL + "/admin/identities"` (and similar). Rewriting every call
// site to thread a *url.URL through is churn for no gain. Instead the
// transport sees the synthetic host, ignores it, and dials the configured
// Unix socket. Any host that *isn't* a registered unix mapping falls
// through to a normal TCP dial — so an http://127.0.0.1:4434 admin URL
// still works (back-compat, kept for tests + operators who haven't
// converted yet).

// unixURLPrefix is the magic prefix that triggers Unix-socket mode in
// configuration. The full form is `unix:/abs/path/to/sock` — no protocol
// scheme, no host. This matches MariaDB's `unix(/path)` DSN convention
// and Kratos's own `host: "unix:/..."` syntax (see install/kratos.yml.tmpl
// admin block) so operators only have to learn one form.
const unixURLPrefix = "unix:"

// parseUnixURL detects the unix:/abs/path form and returns:
//   - syntheticHost: a stable hostname derived from the socket basename,
//     used in the rewritten URL so DialContext can route by host.
//   - sockPath: the absolute filesystem path of the socket.
//   - ok: true iff the input started with unix:.
//
// Returns ("", "", false) for non-unix URLs (the caller keeps the URL as-is).
//
// Examples:
//   unix:/run/jabali-kratos/admin.sock → ("kratos-admin", "/run/jabali-kratos/admin.sock", true)
//   unix:/run/jabali-kratos/public.sock → ("kratos-public", "/run/jabali-kratos/public.sock", true)
//   http://127.0.0.1:4434 → ("", "", false)
func parseUnixURL(rawURL string) (syntheticHost, sockPath string, ok bool) {
	if !strings.HasPrefix(rawURL, unixURLPrefix) {
		return "", "", false
	}
	sockPath = strings.TrimPrefix(rawURL, unixURLPrefix)
	// Allow `unix://abs/path` too — friendlier on operators who write
	// URLs by reflex. Both forms collapse to the same socket path.
	sockPath = strings.TrimPrefix(sockPath, "//")
	if sockPath == "" {
		return "", "", false
	}
	base := sockPath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".sock")
	if base == "" {
		base = "unix"
	}
	// Prefix with "kratos-" so collisions across services are avoided
	// (an upcoming step adds a panel-api unix socket whose synthetic
	// host should not look like a Kratos one).
	return "kratos-" + base, sockPath, true
}

// newKratosTransport builds an http.RoundTripper that dials Unix sockets
// for any host registered in `sockets`, and falls through to a plain TCP
// dial for everything else. Returns the default http.Transport (a fresh
// one — never share with net/http's DefaultTransport so a per-client
// timeout doesn't leak across the binary) when `sockets` is empty.
//
// The dial timeout matches the per-request timeout on the http.Client
// wrapping this transport — there's no benefit to letting a stuck dial
// outlive the request that needs it.
func newKratosTransport(sockets map[string]string, dialTimeout time.Duration) http.RoundTripper {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if len(sockets) == 0 {
		return t
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			// addr without port shouldn't reach DialContext via http.Transport,
			// but be defensive: fall through to a TCP dial so the caller sees
			// the underlying error rather than a confusing "no such host".
			return dialer.DialContext(ctx, network, addr)
		}
		if sock, mapped := sockets[host]; mapped {
			return dialer.DialContext(ctx, "unix", sock)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return t
}

// rewriteForUnix converts a `unix:/abs/path` URL into the synthetic
// `http://<host>` form the transport expects, and registers the host →
// sock-path mapping into `sockets`. Returns the input URL unchanged for
// non-unix URLs (so callers don't have to branch).
//
// This is the single funnel through which a config string crosses into
// the http.Transport's view of the world — keeping the rewriting in one
// place means the synthetic-host scheme can change later without
// hunting through call sites.
func rewriteForUnix(rawURL string, sockets map[string]string) string {
	host, sock, ok := parseUnixURL(rawURL)
	if !ok {
		return rawURL
	}
	sockets[host] = sock
	return "http://" + host
}

// NewReverseProxyTransport is the public adapter used by the panel-api
// /.ory/* same-origin reverse proxy (panel-api/internal/app/kratos_proxy.go).
// It accepts an upstream URL — either http(s):// or unix:/abs/path — and
// returns a (rewritten URL, http.RoundTripper) pair the caller hands to
// httputil.NewSingleHostReverseProxy.
//
// For HTTP/HTTPS URLs the rewritten URL is the input untouched and the
// transport is a fresh default http.Transport (never net/http.DefaultTransport
// — sharing it across two packages would let one's Close() yank the other's
// idle conns). For unix:/path URLs the rewritten URL becomes
// http://<synthetic-host> so net/url.Parse and the reverse proxy's
// host-rewriting code keep working unchanged, and the transport routes that
// host to the configured socket via DialContext.
//
// Returns an error only when the input URL is malformed enough that the
// synthetic-host derivation can't proceed — http(s):// inputs always succeed.
func NewReverseProxyTransport(upstream string) (rewritten string, transport http.RoundTripper, err error) {
	const dialTimeout = 5 * time.Second
	sockets := make(map[string]string, 1)
	rewritten = rewriteForUnix(upstream, sockets)
	if rewritten == "" {
		return "", nil, fmt.Errorf("kratosclient: empty upstream URL")
	}
	return rewritten, newKratosTransport(sockets, dialTimeout), nil
}
