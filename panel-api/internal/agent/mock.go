package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// MockClient is the in-process test double for agent.Client. Tests register
// canned responses per command; each Call records its arguments for later
// assertion. It satisfies AgentInterface so handlers can accept either.
//
// Zero-value MockClient is usable — calls to commands without a registered
// response return an AgentError with code "unknown_command".
type MockClient struct {
	mu        sync.Mutex
	responses map[string]mockResponse
	calls     []MockCall
}

type mockResponse struct {
	data json.RawMessage
	err  error
}

// MockCall is one recorded invocation. Tests inspect it via Calls() to
// assert wire-level behavior (command name, params payload) without going
// through the real transport.
type MockCall struct {
	Command string
	Params  json.RawMessage
}

// NewMockClient returns a ready mock.
func NewMockClient() *MockClient {
	return &MockClient{responses: map[string]mockResponse{}}
}

// On registers a successful response for command. The payload is marshalled
// eagerly so mis-typed responses fail the test setup rather than the call.
func (m *MockClient) On(command string, data any) *MockClient {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("mock: marshal response for %q: %v", command, err))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[command] = mockResponse{data: raw}
	return m
}

// OnError registers a failure response for command. err will be returned
// verbatim, so test code can hand over a *AgentError to simulate typed
// failure or a plain error to simulate transport breakage.
func (m *MockClient) OnError(command string, err error) *MockClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[command] = mockResponse{err: err}
	return m
}

// Call matches agent.Client's signature. Returns a copy of the recorded
// response so callers can't mutate the mock's canned payload.
func (m *MockClient) Call(_ context.Context, command string, params any) (json.RawMessage, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mock: encode params: %w", err)
		}
		rawParams = b
	}

	m.mu.Lock()
	m.calls = append(m.calls, MockCall{Command: command, Params: rawParams})
	resp, ok := m.responses[command]
	m.mu.Unlock()

	if !ok {
		return nil, &AgentError{
			Code:    CodeUnknownCommand,
			Message: fmt.Sprintf("no mock registered for %q", command),
		}
	}
	if resp.err != nil {
		return nil, resp.err
	}
	// defensive copy so callers can't scribble on our canned payload
	out := make(json.RawMessage, len(resp.data))
	copy(out, resp.data)
	return out, nil
}

// Calls returns a snapshot of all invocations to date.
func (m *MockClient) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// Reset clears registered responses and recorded calls. Useful in table
// tests that reuse one mock across cases.
func (m *MockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = map[string]mockResponse{}
	m.calls = nil
}

// Compile-time interface check. If AgentInterface ever gains a method,
// this line fails before any consumer breaks.
var _ AgentInterface = (*MockClient)(nil)
