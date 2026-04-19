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

// TestServiceRestart_RejectsMasked — masked units (e.g. php<v>-fpm per
// ADR-0025) cannot be restarted; surface FailedPrecondition so the API
// can render a helpful message.
func TestServiceRestart_RejectsMasked(t *testing.T) {
	installFakeSystemctl(t, map[string]fakeServiceState{
		"php8.5-fpm": {active: "inactive", loadState: "masked"},
	})

	params, _ := json.Marshal(serviceRestartParams{Name: "php8.5-fpm"})
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
