package commands

import (
	"os/user"
	"testing"
)

// testUserInfo holds cached info about the current test user.
type testUserInfo struct {
	Username string
	UID      string
	GID      string
}

// currentTestUser returns info about the current user running the tests.
// Fails the test if the current user cannot be determined.
func currentTestUser(t *testing.T) testUserInfo {
	u, err := user.Current()
	if err != nil {
		t.Fatalf("failed to get current user: %v", err)
	}
	return testUserInfo{
		Username: u.Username,
		UID:      u.Uid,
		GID:      u.Gid,
	}
}
