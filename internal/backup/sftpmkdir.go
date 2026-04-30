// Package backup — SFTP remote-mkdir helper for M30.1 destinations.
//
// restic's SFTP backend `init` does NOT recursively create the parent
// directory chain on the remote host; it only creates the repo
// sub-layout (data/, keys/, ...) inside an EXISTING parent. Operators
// commonly enter a fresh path that doesn't exist yet, then click Test
// and expect it to "just work". To make that possible, the agent runs
// an explicit `ssh user@host mkdir -p <path>` before `restic init` for
// SFTP destinations.
package backup

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// MkdirRemoteSFTP runs `ssh user@host mkdir -p <path>` over the same
// auth path that restic's sftp.command uses (sshpass for password,
// `-i <key>` for key auth, `-p N` for non-22 ports). extraEnv is
// applied to the child process — typically contains SSHPASS for
// password auth.
//
// Returns combined stderr on error so callers can surface clean
// diagnostics.
func MkdirRemoteSFTP(ctx context.Context, in SFTPInputs, extraEnv []string) ([]byte, error) {
	if in.Host == "" || in.User == "" {
		return nil, fmt.Errorf("sftp: host+user required")
	}
	if in.Path == "" {
		// Empty path means restic will use $HOME on remote — already
		// exists by definition.
		return nil, nil
	}
	args := buildSSHMkdirArgs(in)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(cmd.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("ssh mkdir -p %s: %w (output: %s)",
			in.Path, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// buildSSHMkdirArgs assembles the argv for `[sshpass -e] ssh [-i KEY]
// [-p PORT] [-o ...] user@host mkdir -p PATH`.
func buildSSHMkdirArgs(in SFTPInputs) []string {
	parts := []string{}
	if in.Auth == "password" {
		parts = append(parts, "sshpass", "-e")
	}
	parts = append(parts, "ssh")
	if in.Auth == "key" && in.KeyPath != "" {
		parts = append(parts, "-i", in.KeyPath, "-o", "IdentitiesOnly=yes")
	}
	parts = append(parts, "-o", "StrictHostKeyChecking=accept-new")
	if in.Auth == "password" {
		parts = append(parts, "-o", "PreferredAuthentications=password",
			"-o", "PubkeyAuthentication=no")
	} else {
		parts = append(parts, "-o", "BatchMode=yes")
	}
	if in.Port > 0 && in.Port != 22 {
		parts = append(parts, "-p", fmt.Sprintf("%d", in.Port))
	}
	parts = append(parts, fmt.Sprintf("%s@%s", in.User, in.Host))
	parts = append(parts, "--", "mkdir", "-p", in.Path)
	return parts
}
