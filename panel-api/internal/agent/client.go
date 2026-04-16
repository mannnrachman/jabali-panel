package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
)

// DefaultSocketPath is where install.sh places the socket. Callers override
// it via Config.SocketPath for development or tests.
const DefaultSocketPath = "/run/jabali/agent.sock"

// DefaultTimeout is the per-call wall-clock ceiling when the caller's context
// has no deadline. Chosen generously because many agent commands (cert issue,
// package install) legitimately take 30-60s; hard upper bound avoids runaway
// calls wedging the API.
const DefaultTimeout = 120 * time.Second

// Client is the connection factory + call driver. Safe for concurrent use:
// every Call opens its own connection, so there is no shared mutable state.
//
// The client is intentionally narrow — Call(ctx, command, params) returns
// raw JSON. Typed per-command wrappers belong in feature-specific packages
// that consume them, so changes to one op don't rebuild everything.
type Client struct {
	cfg Config
}

// Config is the tunable surface. Zero values are valid — missing paths
// resolve to DefaultSocketPath, zero Timeout to DefaultTimeout.
type Config struct {
	SocketPath string
	Timeout    time.Duration

	// Dial is injectable so tests can hand the client a net.Pipe pair
	// instead of real sockets. Production code leaves it nil.
	Dial func(ctx context.Context, socketPath string) (net.Conn, error)
}

// AgentInterface is the subset of *Client that callers need. Handlers depend
// on this interface so they can be exercised with a MockClient in tests
// without instantiating the real client.
type AgentInterface interface {
	Call(ctx context.Context, command string, params any) (json.RawMessage, error)
}

// NewClient returns a Client with the supplied config. It does NOT connect
// eagerly — we want main.go to succeed even if the agent isn't running yet,
// so handlers can report a clean "agent unavailable" at call time instead of
// a panic at boot.
func NewClient(cfg Config) *Client {
	if cfg.SocketPath == "" {
		cfg.SocketPath = DefaultSocketPath
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Dial == nil {
		cfg.Dial = dialUnix
	}
	return &Client{cfg: cfg}
}

// dialUnix is the production dialer. Lives as a package-level function so
// tests can override it per-instance via Config.Dial without forcing every
// test to stand up a real socket.
func dialUnix(ctx context.Context, socketPath string) (net.Conn, error) {
	d := net.Dialer{}
	return d.DialContext(ctx, "unix", socketPath)
}

// Call opens a connection, writes one request, reads one response, closes.
// It honours ctx.Deadline() (falls back to cfg.Timeout) and propagates the
// agent's AgentError via errors.As.
//
// params may be any JSON-serialisable value, including nil for commands
// that take no arguments.
func (c *Client) Call(ctx context.Context, command string, params any) (json.RawMessage, error) {
	if command == "" {
		return nil, fmt.Errorf("agent: empty command")
	}

	// Derive a working deadline. If the caller gave us one, use it. Else
	// impose cfg.Timeout so we never block indefinitely on a wedged agent.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}

	conn, err := c.cfg.Dial(ctx, c.cfg.SocketPath)
	if err != nil {
		return nil, wrapDialErr(err)
	}
	// best-effort close; Call does its own proper close below on happy path
	defer conn.Close() //nolint:errcheck // best-effort

	// Apply a single deadline covering both write and read. Individual
	// per-phase deadlines would be more granular but also more to get wrong.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	req, err := buildRequest(ctx, command, params)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: encode request: %w", err)
	}
	// NDJSON framing: one line per message. Terminator is \n.
	if _, err := conn.Write(append(encoded, '\n')); err != nil {
		return nil, fmt.Errorf("agent: write: %w", err)
	}

	// Half-close the write side so the agent can recognise request-complete
	// on older implementations that read to EOF. Harmless under NDJSON.
	if uc, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = uc.CloseWrite()
	}

	// Bound the response we're willing to accept. 8 MiB is overkill for
	// anything the agent should send (largest expected = file-browser
	// metadata listings), but protects us from a runaway or malicious
	// server draining memory.
	const maxResponseBytes = 8 << 20

	scanner := bufio.NewScanner(io.LimitReader(conn, maxResponseBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxResponseBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("agent: read: %w", err)
		}
		return nil, fmt.Errorf("agent: %w: empty response", ErrMalformedResponse)
	}
	line := scanner.Bytes()

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("agent: %w: %w", ErrMalformedResponse, err)
	}
	if resp.ID != "" && resp.ID != req.ID {
		return nil, fmt.Errorf("agent: %w (want %q got %q)", ErrResponseIDMismatch, req.ID, resp.ID)
	}
	if !resp.Ok {
		if resp.Error == nil {
			return nil, fmt.Errorf("agent: %w: ok=false without error object", ErrMalformedResponse)
		}
		return nil, resp.Error
	}
	return resp.Data, nil
}

// buildRequest mints a request envelope with a fresh ULID and propagates the
// deadline from ctx (if any). Extracted so tests can assert on the envelope
// shape without running Call end-to-end.
func buildRequest(ctx context.Context, command string, params any) (*Request, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("agent: encode params: %w", err)
		}
		rawParams = b
	}
	req := &Request{
		ID:      ids.NewULID(),
		Command: command,
		Params:  rawParams,
	}
	if dl, ok := ctx.Deadline(); ok {
		req.Deadline = dl.UTC().Format(time.RFC3339Nano)
	}
	return req, nil
}

// wrapDialErr turns low-level dial failures into an AgentError with code
// "unavailable" so handlers can produce consistent 503-ish responses without
// reflection. Keeps the non-agent sentinels (os.ErrNotExist, timeouts)
// recoverable via errors.Is for callers that care about the root cause.
func wrapDialErr(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &AgentError{
			Code:    CodeUnavailable,
			Message: fmt.Sprintf("agent socket not present: %v", err),
		}
	}
	return &AgentError{
		Code:    CodeUnavailable,
		Message: fmt.Sprintf("dial agent: %v", err),
	}
}
