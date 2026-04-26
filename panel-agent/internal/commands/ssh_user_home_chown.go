package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// ssh.user.home_chown — flip ownership/mode of /home/<user> based on the
// SFTP/SSH access mode. sshd's ChrootDirectory refuses to chroot into a
// path that isn't fully root-owned and not group/world-writable, so SFTP-
// only users need /home/<u> as root:<u> 0751. SSH-shell users need it
// back to <u>:<u> 0750 so they can create files at $HOME.
//
// Subdirectories inside (domains/, .ssh/, .bashrc, etc.) are NEVER touched
// — file perms there are managed by user_create + per-feature commands.
//
// Idempotent: reads current owner+mode and skips chown/chmod when already
// at target. Safe to call from the reconciler every sweep.

type sshUserHomeChownParams struct {
	Username string `json:"username"`
	Mode     string `json:"mode"` // "sftp" or "ssh"
}

type sshUserHomeChownResponse struct {
	Username string `json:"username"`
	Mode     string `json:"mode"`
	Changed  bool   `json:"changed"`
	OwnerUID int    `json:"owner_uid"`
	OwnerGID int    `json:"owner_gid"`
	Perm     string `json:"perm"`
}

func sshUserHomeChownHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sshUserHomeChownParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if p.Username == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username is required",
		}
	}
	if p.Mode != "sftp" && p.Mode != "ssh" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("mode must be 'sftp' or 'ssh', got %q", p.Mode),
		}
	}

	u, err := user.Lookup(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %q not found: %v", p.Username, err),
		}
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("parse uid for %q: %v", p.Username, err),
		}
	}
	primaryGid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("parse gid for %q: %v", p.Username, err),
		}
	}

	// SSH mode keeps group=www-data so nginx (running as www-data) can
	// list /home/<u> with mode 0750. SFTP mode flips to group=<user>
	// because sshd's ChrootDirectory check rejects group-writable paths;
	// 0751 root:<u> is owner=root + group rx (user lists own home) +
	// other --x (nginx still traverses).
	wwwGroup, err := user.LookupGroup("www-data")
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("lookup www-data group: %v", err),
		}
	}
	wwwGid, err := strconv.Atoi(wwwGroup.Gid)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("parse www-data gid: %v", err),
		}
	}

	var wantUID, wantGID int
	var wantMode os.FileMode
	switch p.Mode {
	case "sftp":
		wantUID = 0
		wantGID = primaryGid
		wantMode = 0o751
	case "ssh":
		wantUID = uid
		wantGID = wwwGid
		wantMode = 0o750
	}

	// HomeDir from /etc/passwd is the source of truth — never assume
	// /home/<user> for users with custom homes.
	homeDir := u.HomeDir
	if homeDir == "" || homeDir == "/" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("user %q has no home dir", p.Username),
		}
	}
	homeDir = filepath.Clean(homeDir)

	st, err := os.Stat(homeDir)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("stat %s: %v", homeDir, err),
		}
	}
	if !st.IsDir() {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("%s is not a directory", homeDir),
		}
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "stat sys not *syscall.Stat_t",
		}
	}

	changed := false
	if int(sys.Uid) != wantUID || int(sys.Gid) != wantGID {
		if err := os.Chown(homeDir, wantUID, wantGID); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chown %s: %v", homeDir, err),
			}
		}
		changed = true
	}
	if st.Mode().Perm() != wantMode {
		if err := os.Chmod(homeDir, wantMode); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chmod %s: %v", homeDir, err),
			}
		}
		changed = true
	}

	return &sshUserHomeChownResponse{
		Username: p.Username,
		Mode:     p.Mode,
		Changed:  changed,
		OwnerUID: wantUID,
		OwnerGID: wantGID,
		Perm:     fmt.Sprintf("%#o", wantMode),
	}, nil
}

func init() {
	Default.Register("ssh.user.home_chown", sshUserHomeChownHandler)
}
