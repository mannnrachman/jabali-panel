package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeCopyRunner struct {
	gotName string
	gotArgs []string
	gotEnv  []string
}

func (f *fakeCopyRunner) Run(ctx context.Context, name string, args []string, env []string, stdin io.Reader) ([]byte, []byte, error) {
	f.gotName = name
	f.gotArgs = args
	f.gotEnv = env
	return []byte("ok"), nil, nil
}

func TestCopy_BuildsCorrectArgs(t *testing.T) {
	r := &fakeCopyRunner{}
	_, _, err := Copy(context.Background(), r, CopyOpts{
		FromRepo:         "/var/lib/jabali-backups/repo",
		FromPasswordFile: "/etc/jabali-panel/restic-repo.password",
		ToRepo:           "s3:s3.amazonaws.com/bucket",
		ToPasswordFile:   "/etc/jabali-panel/restic-remotes/x.password",
		Tags:             []Tag{"job-id=01J5"},
	}, []string{"AWS_ACCESS_KEY_ID=x"})
	require.NoError(t, err)
	require.Equal(t, "restic", r.gotName)
	require.Contains(t, r.gotArgs, "copy")
	require.Contains(t, r.gotArgs, "--from-repo")
	require.Contains(t, r.gotArgs, "--from-password-file")
	require.Contains(t, r.gotArgs, "s3:s3.amazonaws.com/bucket")
	require.Contains(t, r.gotEnv, "AWS_ACCESS_KEY_ID=x")
}

func TestLoadEnvFile_ParsesAndSkipsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	body := "# comment\n\nAWS_ACCESS_KEY_ID=AKIA\nAWS_SECRET_ACCESS_KEY=secret\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	got, err := LoadEnvFile(path)
	require.NoError(t, err)
	require.Equal(t, []string{
		"AWS_ACCESS_KEY_ID=AKIA",
		"AWS_SECRET_ACCESS_KEY=secret",
	}, got)
}

func TestLoadEnvFile_MissingFileReturnsNil(t *testing.T) {
	got, err := LoadEnvFile("/nonexistent/path")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestLoadEnvFile_RejectsLineWithoutEquals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(path, []byte("nope\n"), 0o600))
	_, err := LoadEnvFile(path)
	require.Error(t, err)
}
