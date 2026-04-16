package agent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

// ---------- in-process test server ----------

// testAgent is a tiny Unix socket server for client tests. It reads ONE
// NDJSON request per connection, passes it to a handler, writes ONE NDJSON
// response, and closes. Concurrent connections are supported so we can
// stress parallel callers.
type testAgent struct {
	t        *testing.T
	socket   string
	listener net.Listener
	handler  func(req agent.Request) agent.Response

	wg sync.WaitGroup
}

func startTestAgent(t *testing.T, handler func(agent.Request) agent.Response) *testAgent {
	t.Helper()
	dir := t.TempDir()
	// Short path — Unix socket names max 108 bytes on Linux.
	sock := filepath.Join(dir, "a.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)

	s := &testAgent{t: t, socket: sock, listener: l, handler: handler}
	s.wg.Add(1)
	go s.loop()
	t.Cleanup(func() {
		_ = l.Close()
		s.wg.Wait()
	})
	return s
}

func (s *testAgent) loop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.serve(conn)
	}
}

func (s *testAgent) serve(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	if !sc.Scan() {
		return
	}
	var req agent.Request
	if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
		s.t.Logf("testAgent: bad request: %v (%q)", err, sc.Bytes())
		return
	}
	resp := s.handler(req)
	resp.ID = req.ID
	out, _ := json.Marshal(resp)
	_, _ = conn.Write(append(out, '\n'))
}

// ---------- happy-path tests ----------

func TestClient_Call_Success(t *testing.T) {
	t.Parallel()

	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		assert.Equal(t, "agent.version", req.Command)
		assert.JSONEq(t, `{"verbose":true}`, string(req.Params))
		data, _ := json.Marshal(map[string]string{"version": "0.1.0"})
		return agent.Response{Ok: true, Data: data}
	})

	cli := agent.NewClient(agent.Config{SocketPath: ta.socket})
	raw, err := cli.Call(context.Background(), "agent.version", map[string]bool{"verbose": true})
	require.NoError(t, err)

	var out map[string]string
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "0.1.0", out["version"])
}

func TestClient_Call_NilParams(t *testing.T) {
	t.Parallel()

	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		assert.Empty(t, req.Params, "nil params must not appear on the wire")
		return agent.Response{Ok: true}
	})

	cli := agent.NewClient(agent.Config{SocketPath: ta.socket})
	_, err := cli.Call(context.Background(), "agent.version", nil)
	assert.NoError(t, err)
}

func TestClient_Call_PropagatesRequestID(t *testing.T) {
	t.Parallel()

	var seenID string
	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		seenID = req.ID
		return agent.Response{Ok: true}
	})

	cli := agent.NewClient(agent.Config{SocketPath: ta.socket})
	_, err := cli.Call(context.Background(), "agent.version", nil)
	require.NoError(t, err)
	assert.Len(t, seenID, 26, "request ID should be a ULID (26 char)")
}

func TestClient_Call_PropagatesDeadline(t *testing.T) {
	t.Parallel()

	var seenDeadline string
	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		seenDeadline = req.Deadline
		return agent.Response{Ok: true}
	})

	cli := agent.NewClient(agent.Config{SocketPath: ta.socket})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cli.Call(ctx, "agent.version", nil)
	require.NoError(t, err)
	require.NotEmpty(t, seenDeadline)
	parsed, err := time.Parse(time.RFC3339Nano, seenDeadline)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(3*time.Second), parsed, 500*time.Millisecond)
}

// ---------- error paths ----------

func TestClient_Call_AgentErrorPropagated(t *testing.T) {
	t.Parallel()

	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		return agent.Response{
			Ok: false,
			Error: &agent.AgentError{
				Code:    agent.CodeInvalidArgument,
				Message: "username must match ^[a-z][a-z0-9_-]{1,31}$",
			},
		}
	})

	cli := agent.NewClient(agent.Config{SocketPath: ta.socket})
	_, err := cli.Call(context.Background(), "user.create", map[string]string{"name": "!bad"})
	require.Error(t, err)

	var ae *agent.AgentError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, agent.CodeInvalidArgument, ae.Code)
	assert.Contains(t, ae.Message, "username must match")
}

func TestClient_Call_DialFailure_UnavailableCode(t *testing.T) {
	t.Parallel()

	// Use a path that doesn't exist, so the dial must fail with ENOENT.
	cli := agent.NewClient(agent.Config{SocketPath: "/nonexistent/agent.sock"})
	_, err := cli.Call(context.Background(), "agent.version", nil)
	require.Error(t, err)

	var ae *agent.AgentError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, agent.CodeUnavailable, ae.Code)
}

func TestClient_Call_MalformedResponse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sock := filepath.Join(dir, "bad.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain request, respond with garbage.
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.Copy(io.Discard, &limitedReader{r: conn, n: 64 << 10})
		_, _ = conn.Write([]byte("this is not json\n"))
	}()

	cli := agent.NewClient(agent.Config{SocketPath: sock})
	_, err = cli.Call(context.Background(), "agent.version", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, agent.ErrMalformedResponse)
}

func TestClient_Call_ResponseIDMismatch(t *testing.T) {
	t.Parallel()

	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		return agent.Response{Ok: true}
	})
	// Wrap to force an explicit different ID.
	dir := t.TempDir()
	sock := filepath.Join(dir, "idmix.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	_ = ta // referenced for lint; unused

	go func() {
		conn, _ := l.Accept()
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		sc.Scan() // drain
		_, _ = conn.Write([]byte(`{"id":"SOMETHING-ELSE","ok":true}` + "\n"))
	}()

	cli := agent.NewClient(agent.Config{SocketPath: sock})
	_, err = cli.Call(context.Background(), "agent.version", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, agent.ErrResponseIDMismatch)
}

func TestClient_Call_Timeout(t *testing.T) {
	t.Parallel()

	// Server accepts but never writes — client's deadline must fire.
	dir := t.TempDir()
	sock := filepath.Join(dir, "slow.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		conn, _ := l.Accept()
		if conn != nil {
			time.Sleep(2 * time.Second)
			_ = conn.Close()
		}
	}()

	cli := agent.NewClient(agent.Config{SocketPath: sock, Timeout: 200 * time.Millisecond})
	start := time.Now()
	_, err = cli.Call(context.Background(), "agent.version", nil)
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.Less(t, elapsed, 1*time.Second, "should return well before 2s server sleep")
}

// ---------- concurrency ----------

func TestClient_Call_ConcurrentCallsIndependent(t *testing.T) {
	t.Parallel()

	ta := startTestAgent(t, func(req agent.Request) agent.Response {
		// Echo the params so each goroutine can verify isolation.
		return agent.Response{Ok: true, Data: req.Params}
	})

	cli := agent.NewClient(agent.Config{SocketPath: ta.socket})

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			raw, err := cli.Call(context.Background(), "echo", map[string]int{"n": i})
			if !assert.NoError(t, err) {
				return
			}
			var out map[string]int
			require.NoError(t, json.Unmarshal(raw, &out))
			assert.Equal(t, i, out["n"])
		}()
	}
	wg.Wait()
}

// ---------- tiny helper ----------

// limitedReader caps the number of bytes pulled from r — we use it in tests
// so a runaway test server doesn't hang Copy on an agent that never closes.
type limitedReader struct {
	r io.Reader
	n int
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, io.EOF
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= n
	return n, err
}
