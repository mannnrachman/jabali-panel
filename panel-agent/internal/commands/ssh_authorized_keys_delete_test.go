package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHAuthorizedKeysDelete_InvalidUser(t *testing.T) {
	ctx := context.Background()

	params, _ := json.Marshal(sshAuthorizedKeysDeleteParams{
		Username: "nonexistent-user-xyz-12345",
	})

	_, err := sshAuthorizedKeysDeleteHandler(ctx, params)
	require.Error(t, err, "should reject nonexistent user")
}

func TestSSHAuthorizedKeysDelete_InvalidJSON(t *testing.T) {
	ctx := context.Background()

	_, err := sshAuthorizedKeysDeleteHandler(ctx, []byte("invalid json"))
	require.Error(t, err, "should reject invalid JSON")
}
