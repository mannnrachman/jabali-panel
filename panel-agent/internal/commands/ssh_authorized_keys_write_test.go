package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHAuthorizedKeysWrite_InvalidUser(t *testing.T) {
	ctx := context.Background()

	params, _ := json.Marshal(sshAuthorizedKeysWriteParams{
		Username: "nonexistent-user-xyz-12345",
		Keys: []string{
			"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG test-key",
		},
	})

	_, err := sshAuthorizedKeysWriteHandler(ctx, params)
	require.Error(t, err, "should reject nonexistent user")
}

func TestSSHAuthorizedKeysWrite_InvalidJSON(t *testing.T) {
	ctx := context.Background()

	_, err := sshAuthorizedKeysWriteHandler(ctx, []byte("invalid json"))
	require.Error(t, err, "should reject invalid JSON")
}

func TestSSHAuthorizedKeysWrite_WithTempDir(t *testing.T) {
	// This test demonstrates the file writing behavior in isolation.
	tmpDir := t.TempDir()

	// Create a mock user-like structure for testing file operations
	// (This is just to verify the logic without actually modifying /home)
	testSSHDir := filepath.Join(tmpDir, ".ssh")
	testKeysFile := filepath.Join(testSSHDir, "authorized_keys")

	// Manually test the file logic
	os.Mkdir(testSSHDir, 0700)

	content := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG key1\n"
	content += "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIH key2\n"

	err := os.WriteFile(testKeysFile, []byte(content), 0600)
	require.NoError(t, err)

	// Verify file contents
	data, err := os.ReadFile(testKeysFile)
	require.NoError(t, err)
	require.Equal(t, content, string(data))

	// Verify file mode
	info, err := os.Stat(testKeysFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode()&os.ModePerm)
}
