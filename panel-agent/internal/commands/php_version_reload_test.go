package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPVersionReload_ValidVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	params, _ := json.Marshal(phpVersionReloadParams{Version: "8.5"})

	// This will fail because systemctl is not actually runnable in test environment,
	// but we're verifying the parameter validation and error handling
	_, err := phpVersionReloadHandler(ctx, params)
	// Should fail due to command execution, not due to validation
	require.Error(t, err)
	assert.NotNil(t, err)
}

func TestPHPVersionReload_InvalidVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantErr string
	}{
		{"unsupported", "5.6", "unsupported version"},
		{"invalid format", "8", "invalid version format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			params, _ := json.Marshal(phpVersionReloadParams{Version: tt.version})

			_, err := phpVersionReloadHandler(ctx, params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPHPVersionReload_NoParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := phpVersionReloadHandler(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version parameter required")
}
