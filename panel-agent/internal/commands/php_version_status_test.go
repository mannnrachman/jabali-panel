package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPVersionStatus_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	resp, err := phpVersionStatusHandler(ctx, nil)
	require.NoError(t, err)

	result, ok := resp.(phpVersionStatusResponse)
	require.True(t, ok, "response should be phpVersionStatusResponse")

	assert.Equal(t, "8.5", result.DefaultVersion)
	assert.Len(t, result.Versions, len(SupportedPHPVersions))

	// Verify the structure is correct
	for i, detail := range result.Versions {
		assert.Equal(t, SupportedPHPVersions[i], detail.Version)
		// Verify all fields are present (values depend on actual system state)
		assert.NotNil(t, detail.Version)
		assert.NotNil(t, detail.Installed)
		assert.NotNil(t, detail.FPMRunning)
	}
}

func TestPHPVersionStatus_WithInvalidParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	invalidParams := json.RawMessage(`{invalid json}`)

	_, err := phpVersionStatusHandler(ctx, invalidParams)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse params")
}
