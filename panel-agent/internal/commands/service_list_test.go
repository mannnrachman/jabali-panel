package commands

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceListHandler_WithMockSystemctl(t *testing.T) {
	t.Parallel()

	// Save and restore global state.
	origRunner := systemctlRunner
	origServices := AllowedServices
	t.Cleanup(func() {
		systemctlRunner = origRunner
		AllowedServices = origServices
	})

	AllowedServices = []string{"nginx", "mariadb", "fake-missing"}

	// Mock: nginx active, mariadb inactive, fake-missing unknown
	systemctlRunner = func(_ context.Context, args ...string) (string, error) {
		if len(args) < 2 {
			return "", nil
		}
		switch args[1] {
		case "nginx.service":
			return "active", nil
		case "mariadb.service":
			return "inactive", errors.New("exit status 3")
		default:
			return "", errors.New("exit status 4")
		}
	}

	r := NewRegistry()
	r.Register("service.list", serviceListHandler)

	data, agentErr := r.Dispatch(context.Background(), "service.list", nil)
	require.Nil(t, agentErr)

	var resp ServiceListResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	require.Len(t, resp.Services, 3)

	assert.Equal(t, "nginx", resp.Services[0].Name)
	assert.Equal(t, "active", resp.Services[0].Active)

	assert.Equal(t, "mariadb", resp.Services[1].Name)
	assert.Equal(t, "inactive", resp.Services[1].Active)

	assert.Equal(t, "fake-missing", resp.Services[2].Name)
	assert.Equal(t, "unknown", resp.Services[2].Active)
}

func TestIsServiceNameChar(t *testing.T) {
	t.Parallel()

	// Valid characters
	for _, c := range "abcABC019-._@" {
		assert.True(t, isServiceNameChar(c), "should allow %c", c)
	}
	// Injection characters
	for _, c := range ";&|$`'\"()/\\ " {
		assert.False(t, isServiceNameChar(c), "should reject %c", c)
	}
}
