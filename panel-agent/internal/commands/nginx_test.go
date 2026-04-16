package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNginxTestHandler_Success(t *testing.T) {
	t.Skip("requires root + nginx")

	_, err := nginxTestHandler(context.Background(), json.RawMessage([]byte("{}")))
	require.NoError(t, err)
}

func TestNginxReloadHandler_Success(t *testing.T) {
	t.Skip("requires root + nginx")

	_, err := nginxReloadHandler(context.Background(), json.RawMessage([]byte("{}")))
	require.NoError(t, err)
}
