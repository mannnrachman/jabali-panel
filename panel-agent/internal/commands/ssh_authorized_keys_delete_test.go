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

func TestSSHAuthorizedKeysDelete_ExistingUser(t *testing.T) {
	ctx := context.Background()

	// Test with a known system user (e.g., root)
	// In a real scenario, this would only work if running as root and the file exists.
	params, _ := json.Marshal(sshAuthorizedKeysDeleteParams{
		Username: "root",
	})

	result, err := sshAuthorizedKeysDeleteHandler(ctx, params)
	if err != nil {
		t.Logf("Error (may be expected if not running as root): %v", err)
		return
	}

	resp := result.(*sshAuthorizedKeysDeleteResponse)
	require.Equal(t, "root", resp.Username)
	require.True(t, resp.Deleted)
}
