package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

func TestMockClient_ReturnsRegisteredData(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().On("agent.version", map[string]string{"version": "0.1.0"})

	raw, err := m.Call(context.Background(), "agent.version", nil)
	require.NoError(t, err)

	var out map[string]string
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "0.1.0", out["version"])
}

func TestMockClient_ReturnsRegisteredError(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().OnError("user.create", &agent.AgentError{
		Code:    agent.CodeAlreadyExists,
		Message: "duplicate user",
	})

	_, err := m.Call(context.Background(), "user.create", map[string]string{"name": "bob"})
	require.Error(t, err)

	var ae *agent.AgentError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, agent.CodeAlreadyExists, ae.Code)
}

func TestMockClient_UnknownCommandReturnsTypedError(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient()
	_, err := m.Call(context.Background(), "not.registered", nil)

	var ae *agent.AgentError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, agent.CodeUnknownCommand, ae.Code)
}

func TestMockClient_CallsRecorded(t *testing.T) {
	t.Parallel()

	m := agent.NewMockClient().On("a", 1).On("b", 2)
	_, _ = m.Call(context.Background(), "a", map[string]int{"x": 1})
	_, _ = m.Call(context.Background(), "b", map[string]int{"y": 2})

	calls := m.Calls()
	require.Len(t, calls, 2)
	assert.Equal(t, "a", calls[0].Command)
	assert.JSONEq(t, `{"x":1}`, string(calls[0].Params))
	assert.Equal(t, "b", calls[1].Command)
}

func TestMockClient_DataCopyIsolation(t *testing.T) {
	t.Parallel()

	// Ensure callers can't mutate the registered payload via the returned
	// slice. A regression here would cross-contaminate tests reusing a mock.
	m := agent.NewMockClient().On("x", map[string]string{"k": "v"})
	raw1, err := m.Call(context.Background(), "x", nil)
	require.NoError(t, err)
	// scribble
	for i := range raw1 {
		raw1[i] = '!'
	}
	raw2, err := m.Call(context.Background(), "x", nil)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2))
}
