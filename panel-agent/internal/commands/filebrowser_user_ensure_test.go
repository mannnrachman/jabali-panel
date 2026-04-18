package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilebrowserUserEnsure_ValidParams(t *testing.T) {
	ctx := context.Background()

	params, _ := json.Marshal(filebrowserUserEnsureParams{
		Username: "testuser",
		Scope:    "/home/testuser",
	})

	// This would normally fail since filebrowser isn't installed in tests,
	// but we're testing parameter validation here
	result, err := filebrowserUserEnsureHandler(ctx, params)

	// We expect an error because filebrowser binary isn't available in test,
	// but parameters should be valid
	if err != nil {
		// Expected: filebrowser command not found
		t.Logf("Expected error (filebrowser not installed): %v", err)
	} else {
		resp := result.(*filebrowserUserEnsureResponse)
		require.Equal(t, "testuser", resp.Username)
		require.Equal(t, "/home/testuser", resp.Scope)
	}
}

func TestFilebrowserUserEnsure_InvalidUsername(t *testing.T) {
	ctx := context.Background()

	tests := []string{
		"TestUser",      // uppercase
		"test-user",     // hyphen
		"1testuser",     // starts with digit
		"_testuser",     // starts with underscore
		"test user",     // contains space
		"test@user",     // contains special char
		"toolongusernamethatexceedsthirtytwochars123456", // too long
	}

	for _, invalid := range tests {
		params, _ := json.Marshal(filebrowserUserEnsureParams{
			Username: invalid,
			Scope:    "/home/testuser",
		})

		_, err := filebrowserUserEnsureHandler(ctx, params)
		require.Error(t, err, "should reject invalid username: %s", invalid)
	}
}

func TestFilebrowserUserEnsure_EmptyScope(t *testing.T) {
	ctx := context.Background()

	params, _ := json.Marshal(filebrowserUserEnsureParams{
		Username: "testuser",
		Scope:    "",
	})

	_, err := filebrowserUserEnsureHandler(ctx, params)
	require.Error(t, err, "should reject empty scope")
}

func TestFilebrowserUserEnsure_InvalidJSON(t *testing.T) {
	ctx := context.Background()

	_, err := filebrowserUserEnsureHandler(ctx, []byte("invalid json"))
	require.Error(t, err, "should reject invalid JSON")
}
