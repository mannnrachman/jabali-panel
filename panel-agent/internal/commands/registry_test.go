package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/commands"
)

func TestRegistry_DispatchUnknown(t *testing.T) {
	t.Parallel()

	r := commands.NewRegistry()
	_, aerr := r.Dispatch(context.Background(), "nope", nil)
	require.NotNil(t, aerr)
	assert.Equal(t, agentwire.CodeUnknownCommand, aerr.Code)
}

func TestRegistry_DispatchSuccess(t *testing.T) {
	t.Parallel()

	r := commands.NewRegistry()
	r.Register("ping", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"pong": "yes"}, nil
	})

	raw, aerr := r.Dispatch(context.Background(), "ping", nil)
	require.Nil(t, aerr)
	assert.JSONEq(t, `{"pong":"yes"}`, string(raw))
}

func TestRegistry_DispatchTypedError(t *testing.T) {
	t.Parallel()

	r := commands.NewRegistry()
	r.Register("fail", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "bad arg"}
	})

	_, aerr := r.Dispatch(context.Background(), "fail", nil)
	require.NotNil(t, aerr)
	assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
}

func TestRegistry_DispatchPlainErrorBecomesInternal(t *testing.T) {
	t.Parallel()

	r := commands.NewRegistry()
	r.Register("kaboom", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, errors.New("disk on fire")
	})

	_, aerr := r.Dispatch(context.Background(), "kaboom", nil)
	require.NotNil(t, aerr)
	assert.Equal(t, agentwire.CodeInternal, aerr.Code)
	assert.Contains(t, aerr.Message, "disk on fire")
}

func TestRegistry_DuplicateRegistrationPanics(t *testing.T) {
	t.Parallel()

	r := commands.NewRegistry()
	r.Register("dup", func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })

	assert.Panics(t, func() {
		r.Register("dup", func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })
	})
}

func TestRegistry_CommandsSorted(t *testing.T) {
	t.Parallel()

	r := commands.NewRegistry()
	r.Register("b", func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })
	r.Register("a", func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })
	r.Register("c", func(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil })

	assert.Equal(t, []string{"a", "b", "c"}, r.Commands())
}

func TestAgentVersion_RegisteredByInit(t *testing.T) {
	t.Parallel()

	// The init() in agent_version.go registered into commands.Default.
	raw, aerr := commands.Default.Dispatch(context.Background(), "agent.version", nil)
	require.Nil(t, aerr)

	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.NotEmpty(t, out["version"])
	assert.NotEmpty(t, out["go_version"])
	assert.NotEmpty(t, out["started_at"])
}
