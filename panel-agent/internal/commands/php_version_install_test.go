package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPVersionInstall_InvalidVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantErr string
	}{
		{"unsupported", "5.6", "unsupported version"},
		{"invalid format", "8", "invalid version format"},
		{"invalid format dotted", "8.1.0", "invalid version format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			params, _ := json.Marshal(phpVersionInstallParams{Version: tt.version})

			_, err := phpVersionInstallHandler(ctx, params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPHPVersionInstall_NoParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := phpVersionInstallHandler(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version parameter required")
}

func TestVersionSupportValidation(t *testing.T) {
	t.Parallel()

	for _, v := range SupportedPHPVersions {
		assert.True(t, isVersionSupported(v), "version %s should be supported", v)
	}

	assert.False(t, isVersionSupported("5.6"))
	assert.False(t, isVersionSupported("9.0"))
}
