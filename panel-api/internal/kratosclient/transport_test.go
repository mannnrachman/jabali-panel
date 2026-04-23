package kratosclient

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// M25 Step 2 — transport tests.
//
// The unit-of-test here is the transport plumbing: parseUnixURL,
// rewriteForUnix, newKratosTransport, and the NewClient integration that
// wires them together. We verify both the parse layer (pure-functional,
// fast) and the live-dial layer (end-to-end via a unix-socket http server
// in a temp directory).

func TestParseUnixURL_DetectsPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantHost string
		wantSock string
		wantOK   bool
	}{
		{"basic admin socket", "unix:/run/jabali-kratos/admin.sock", "kratos-admin", "/run/jabali-kratos/admin.sock", true},
		{"basic public socket", "unix:/run/jabali-kratos/public.sock", "kratos-public", "/run/jabali-kratos/public.sock", true},
		{"double-slash form", "unix:///tmp/foo.sock", "kratos-foo", "/tmp/foo.sock", true},
		{"socket without .sock suffix", "unix:/var/run/somesock", "kratos-somesock", "/var/run/somesock", true},
		{"http url ignored", "http://127.0.0.1:4434", "", "", false},
		{"https url ignored", "https://auth.example.com", "", "", false},
		{"empty string ignored", "", "", "", false},
		{"unix prefix without path", "unix:", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, sock, ok := parseUnixURL(tt.input)
			assert.Equal(t, tt.wantOK, ok, "ok flag")
			assert.Equal(t, tt.wantHost, host, "synthetic host")
			assert.Equal(t, tt.wantSock, sock, "socket path")
		})
	}
}

func TestRewriteForUnix_RegistersSocketAndRewrites(t *testing.T) {
	t.Parallel()

	sockets := make(map[string]string)
	out := rewriteForUnix("unix:/run/jabali-kratos/admin.sock", sockets)

	assert.Equal(t, "http://kratos-admin", out, "unix URL rewritten to synthetic http URL")
	assert.Equal(t, "/run/jabali-kratos/admin.sock", sockets["kratos-admin"], "socket path registered")
}

func TestRewriteForUnix_PassthroughForHTTPURL(t *testing.T) {
	t.Parallel()

	sockets := make(map[string]string)
	out := rewriteForUnix("http://127.0.0.1:4434", sockets)

	assert.Equal(t, "http://127.0.0.1:4434", out, "non-unix URL returned unchanged")
	assert.Empty(t, sockets, "non-unix URL adds no entry")
}

// TestNewKratosTransport_DialsUnixSocket boots a tiny http server on a
// real Unix socket and verifies that an http.Client built around the
// returned transport reaches it via DialContext routing. This is the
// closest we can get to the production code path without launching
// Kratos itself.
func TestNewKratosTransport_DialsUnixSocket(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "kratos-admin.sock")

	listener, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	transport := newKratosTransport(map[string]string{"kratos-admin": sockPath}, 2*time.Second)
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}

	req, err := http.NewRequest(http.MethodGet, "http://kratos-admin/admin/health/ready", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestNewKratosTransport_PassthroughForUnregisteredHost verifies that a
// host that isn't in the unix-socket map falls through to a normal TCP
// dial. We can't easily prove "TCP dial" without binding a real port, so
// instead we assert the dial *fails* with a connection-refused-style
// error against a deliberately unreachable host:port — the absence of
// "no such file or directory" (the unix-socket failure mode) confirms
// the dialer didn't try the unix path.
func TestNewKratosTransport_PassthroughForUnregisteredHost(t *testing.T) {
	t.Parallel()

	transport := newKratosTransport(map[string]string{"kratos-admin": "/tmp/nope.sock"}, 500*time.Millisecond)
	client := &http.Client{Transport: transport, Timeout: 1 * time.Second}

	// 127.0.0.1:1 is the well-known "definitely not listening" trick.
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/x", nil)
	require.NoError(t, err)
	_, err = client.Do(req)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "no such file or directory",
		"unregistered host must not be dialed as a unix socket")
}

// TestNewClient_UnixSocketIntegration verifies the full NewClient path:
// pass a unix:/path admin URL, the resulting client must reach a real
// unix-socket-bound server when calling AdminReady.
func TestNewClient_UnixSocketIntegration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "admin.sock")

	listener, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := NewClient("http://localhost:9999", "unix:"+sockPath)
	require.NoError(t, client.AdminReady(context.Background()))

	// And the file at sockPath exists — sanity check that we built
	// the transport against the right path.
	_, err = os.Stat(sockPath)
	require.NoError(t, err)
}

// TestNewClient_HTTPURLBackcompat verifies that a plain http:// URL keeps
// working — operators on legacy TCP setups (or tests) shouldn't need to
// migrate atomically.
func TestNewClient_HTTPURLBackcompat(t *testing.T) {
	t.Parallel()

	// Boot a tiny TCP server.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/health/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	url := "http://" + listener.Addr().String()
	client := NewClient(url, url)

	require.NoError(t, client.AdminReady(context.Background()))
}
