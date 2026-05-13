package commands

// Tests for user.suspend / user.unsuspend agent commands.
//
// Full exec validation (gpasswd / usermod actually flipping state)
// is verified by a live-VM smoke; tests here cover the parameter
// validation + handler wiring that doesn't require root + a real
// jabali-sftp group.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestUserSuspendHandler_InvalidJSON(t *testing.T) {
	_, err := userSuspendHandler(context.Background(), []byte("not-json"))
	require.Error(t, err)
	ae, ok := err.(*agentwire.AgentError)
	require.True(t, ok)
	require.Equal(t, agentwire.CodeInvalidArgument, ae.Code)
}

func TestUserSuspendHandler_RejectsBadUsername(t *testing.T) {
	cases := []string{
		"",                                   // empty fails len check
		"root user",                          // space rejected by regex
		"u$er",                               // special char rejected
		"UPPERCASE",                          // uppercase rejected
		"toolongnameexceedsthethirtytwochars0", // >32 chars
	}
	for _, u := range cases {
		raw, _ := json.Marshal(userSuspendParams{Username: u})
		_, err := userSuspendHandler(context.Background(), raw)
		require.Error(t, err, "should reject %q", u)
		ae, ok := err.(*agentwire.AgentError)
		require.True(t, ok, "wrong error type for %q", u)
		require.Equal(t, agentwire.CodeInvalidArgument, ae.Code, "wrong code for %q", u)
	}
}

func TestUserUnsuspendHandler_InvalidJSON(t *testing.T) {
	_, err := userUnsuspendHandler(context.Background(), []byte("{not}"))
	require.Error(t, err)
	ae, ok := err.(*agentwire.AgentError)
	require.True(t, ok)
	require.Equal(t, agentwire.CodeInvalidArgument, ae.Code)
}

func TestUserUnsuspendHandler_RejectsBadUsername(t *testing.T) {
	raw, _ := json.Marshal(userSuspendParams{Username: "with space"})
	_, err := userUnsuspendHandler(context.Background(), raw)
	require.Error(t, err)
	ae, ok := err.(*agentwire.AgentError)
	require.True(t, ok)
	require.Equal(t, agentwire.CodeInvalidArgument, ae.Code)
}
