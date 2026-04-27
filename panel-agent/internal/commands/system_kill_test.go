package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemKill_RejectsDenylistComm(t *testing.T) {
	dir := t.TempDir()
	pidDir := filepath.Join(dir, "5000")
	require.NoError(t, os.MkdirAll(pidDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pidDir, "stat"), []byte("5000 (sshd) S 1 1 1 0"), 0o644))

	t.Cleanup(func(orig string) func() { return func() { procDir = orig } }(procDir))
	procDir = dir

	_, err := systemKillHandler(context.Background(), json.RawMessage(`{"pid":5000}`))
	assert.Error(t, err, "sshd should be denied")
}

func TestSystemKill_RejectsPID1(t *testing.T) {
	_, err := systemKillHandler(context.Background(), json.RawMessage(`{"pid":1}`))
	assert.Error(t, err, "pid 1 should be denied")
}

func TestSystemKill_RejectsMissingPID(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func(orig string) func() { return func() { procDir = orig } }(procDir))
	procDir = dir

	_, err := systemKillHandler(context.Background(), json.RawMessage(`{"pid":99999}`))
	assert.Error(t, err, "missing pid should be not_found")
}

func TestSystemKill_RejectsBadParams(t *testing.T) {
	_, err := systemKillHandler(context.Background(), json.RawMessage(``))
	assert.Error(t, err)
	_, err = systemKillHandler(context.Background(), json.RawMessage(`{"pid":-1}`))
	assert.Error(t, err)
}
