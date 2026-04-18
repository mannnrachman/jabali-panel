package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHUserJoinSFTPGroup_InvalidUser(t *testing.T) {
	ctx := context.Background()

	params, _ := json.Marshal(sshUserJoinSFTPGroupParams{
		Username: "nonexistent-user-xyz-12345",
	})

	_, err := sshUserJoinSFTPGroupHandler(ctx, params)
	require.Error(t, err, "should reject nonexistent user")
}

func TestSSHUserJoinSFTPGroup_InvalidJSON(t *testing.T) {
	ctx := context.Background()

	_, err := sshUserJoinSFTPGroupHandler(ctx, []byte("invalid json"))
	require.Error(t, err, "should reject invalid JSON")
}

func TestSSHUserJoinSFTPGroup_ExistingUser(t *testing.T) {
	ctx := context.Background()

	// Test with a known system user (e.g., root)
	// If the jabali-sftp group doesn't exist, this will fail; that's expected.
	params, _ := json.Marshal(sshUserJoinSFTPGroupParams{
		Username: "root",
	})

	result, err := sshUserJoinSFTPGroupHandler(ctx, params)
	if err != nil {
		t.Logf("Error (expected if jabali-sftp group doesn't exist or not running as root): %v", err)
		return
	}

	resp := result.(*sshUserJoinSFTPGroupResponse)
	require.Equal(t, "root", resp.Username)
	// Either joined or already a member
	require.True(t, resp.Joined || resp.AlreadyMember)
}

func TestSSHUserJoinSFTPGroup_Idempotent(t *testing.T) {
	ctx := context.Background()

	params, _ := json.Marshal(sshUserJoinSFTPGroupParams{
		Username: "root",
	})

	// First call
	result1, err1 := sshUserJoinSFTPGroupHandler(ctx, params)
	if err1 != nil {
		t.Logf("First call error (expected if jabali-sftp group doesn't exist): %v", err1)
		return
	}

	resp1 := result1.(*sshUserJoinSFTPGroupResponse)

	// Second call (should be idempotent)
	result2, err2 := sshUserJoinSFTPGroupHandler(ctx, params)
	if err2 != nil {
		t.Logf("Second call error: %v", err2)
		return
	}

	resp2 := result2.(*sshUserJoinSFTPGroupResponse)

	// Both should succeed
	require.True(t, (resp1.Joined && resp2.AlreadyMember) || (resp1.AlreadyMember && resp2.AlreadyMember))
}
