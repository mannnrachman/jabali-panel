package backup

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComposeSFTPURL(t *testing.T) {
	require.Equal(t, "sftp:bob@host:/repo", ComposeSFTPURL(SFTPInputs{
		Host: "host", User: "bob", Path: "/repo",
	}))
	require.Equal(t, "sftp:bob@host:", ComposeSFTPURL(SFTPInputs{
		Host: "host", User: "bob",
	}))
	require.Equal(t, "", ComposeSFTPURL(SFTPInputs{Host: "host"}))
}

func TestSFTPCommandFlag_DefaultsAreEmpty(t *testing.T) {
	got := SFTPCommandFlag(SFTPInputs{Host: "h", User: "u", Path: "/r"})
	require.Empty(t, got, "default ssh config should not need an override")
}

func TestSFTPCommandFlag_KeyPath(t *testing.T) {
	got := SFTPCommandFlag(SFTPInputs{
		Host: "h", User: "u", Path: "/r", Auth: "key",
		KeyPath: "/root/.ssh/id_rsa",
	})
	require.Contains(t, got, "sftp.command=ssh -i /root/.ssh/id_rsa")
	require.Contains(t, got, "u@h")
	require.Contains(t, got, "-s sftp")
}

func TestSFTPCommandFlag_Password(t *testing.T) {
	got := SFTPCommandFlag(SFTPInputs{
		Host: "h", User: "u", Path: "/r", Auth: "password",
	})
	require.True(t, strings.HasPrefix(got, "sftp.command=sshpass -e ssh"))
	require.Contains(t, got, "u@h")
}

func TestSFTPCommandFlag_NonStandardPort(t *testing.T) {
	got := SFTPCommandFlag(SFTPInputs{
		Host: "h", User: "u", Path: "/r", Auth: "key",
		KeyPath: "/root/.ssh/id_ed25519", Port: 2222,
	})
	require.Contains(t, got, "-p 2222")
	require.Contains(t, got, "-i /root/.ssh/id_ed25519")
}
