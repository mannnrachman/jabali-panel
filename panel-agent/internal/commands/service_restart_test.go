package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestServiceRestart_OK(t *testing.T) {
	state := map[string]fakeServiceState{
		"nginx": {active: "inactive", loadState: "loaded"},
	}
	installFakeSystemctl(t, state)

	params, _ := json.Marshal(serviceRestartParams{Name: "nginx"})
	out, err := serviceRestartHandler(context.Background(), params)
	require.NoError(t, err)

	resp := out.(serviceRestartResponse)
	assert.Equal(t, "nginx", resp.Name)
	assert.Equal(t, "active", resp.Active, "fake restart should flip active")
	assert.Equal(t, "loaded", resp.LoadState)
}

// TestServiceRestart_RejectsMasked — masked units cannot be restarted;
// surface FailedPrecondition so the API can render a helpful message.
// We use stalwart-mail as the test subject: it's in the allow-list, so
// the handler gets past the allow-list gate and reaches the mask check.
// (Global php<v>-fpm was removed from the allow-list per ADR-0025 —
// those are always masked and never restartable by design.)
func TestServiceRestart_RejectsMasked(t *testing.T) {
	installFakeSystemctl(t, map[string]fakeServiceState{
		"stalwart-mail": {active: "inactive", loadState: "masked"},
	})

	params, _ := json.Marshal(serviceRestartParams{Name: "stalwart-mail"})
	_, err := serviceRestartHandler(context.Background(), params)

	require.Error(t, err)
	ae, ok := err.(*agentwire.AgentError)
	require.True(t, ok, "expected AgentError, got %T", err)
	assert.Equal(t, agentwire.CodeFailedPrecondition, ae.Code)
	assert.Contains(t, ae.Message, "masked")
}

func TestServiceRestart_RejectsNotInstalled(t *testing.T) {
	installFakeSystemctl(t, map[string]fakeServiceState{})

	params, _ := json.Marshal(serviceRestartParams{Name: "nginx"})
	_, err := serviceRestartHandler(context.Background(), params)

	require.Error(t, err)
	ae, ok := err.(*agentwire.AgentError)
	require.True(t, ok)
	assert.Equal(t, agentwire.CodeNotFound, ae.Code)
}

// TestServiceRestart_RejectsOffAllowList — a compromised panel must not
// be able to restart arbitrary systemd units.
func TestServiceRestart_RejectsOffAllowList(t *testing.T) {
	installFakeSystemctl(t, map[string]fakeServiceState{
		"sshd": {active: "active", loadState: "loaded"},
	})

	params, _ := json.Marshal(serviceRestartParams{Name: "sshd"})
	_, err := serviceRestartHandler(context.Background(), params)

	require.Error(t, err)
	ae, ok := err.(*agentwire.AgentError)
	require.True(t, ok)
	assert.Equal(t, agentwire.CodePermissionDenied, ae.Code)
	assert.Contains(t, ae.Message, "allow-list")
}

func TestServiceRestart_InvalidInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
		wantMsg string
	}{
		{"empty_body", ``, "params required"},
		{"malformed", `{not json`, "parse params"},
		{"empty_name", `{"name":"   "}`, "name required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := serviceRestartHandler(context.Background(), json.RawMessage(tc.payload))
			require.Error(t, err)
			ae, ok := err.(*agentwire.AgentError)
			require.True(t, ok)
			assert.Equal(t, agentwire.CodeInvalidArgument, ae.Code)
			assert.Contains(t, ae.Message, tc.wantMsg)
		})
	}
}

func TestServiceRestart_Registered(t *testing.T) {
	t.Parallel()
	for _, name := range Default.Commands() {
		if name == "service.restart" {
			return
		}
	}
	t.Fatal("service.restart not registered")
}
