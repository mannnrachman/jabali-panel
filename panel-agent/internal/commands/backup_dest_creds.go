// M30.1 follow-up — agent-side write of backup_destinations creds env
// files. /etc/jabali-panel/restic-remotes/<id>.env is created at
// install time with root:root 0700; panel-api (jabali user) cannot
// write into /etc under ProtectSystem=strict, so writes go via this
// agent command.
//
// Commands:
//   backup.dest.creds_write   — atomic write of <id>.env (0600 root:root)
//   backup.dest.creds_delete  — remove <id>.env
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const credsDir = "/etc/jabali-panel/restic-remotes"

type credsWriteParams struct {
	DestID string            `json:"dest_id"`
	Env    map[string]string `json:"env"`
}

type credsWriteResult struct {
	Path string `json:"path"`
}

func backupDestCredsWriteHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p credsWriteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid_arg: %w", err)
	}
	if !validULID(p.DestID) {
		return nil, fmt.Errorf("invalid_arg: dest_id must be a 26-char ULID")
	}
	if len(p.Env) == 0 {
		return nil, fmt.Errorf("invalid_arg: env map empty")
	}
	for k, v := range p.Env {
		if !validCredsKey(k) {
			return nil, fmt.Errorf("invalid_arg: bad env key %q", k)
		}
		if strings.ContainsAny(v, "\n\r") {
			return nil, fmt.Errorf("invalid_arg: env value for %q contains newline", k)
		}
	}
	if err := os.MkdirAll(credsDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", credsDir, err)
	}
	final := filepath.Join(credsDir, p.DestID+".env")
	tmp, err := os.CreateTemp(credsDir, p.DestID+".env.*")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, fmt.Errorf("chmod: %w", err)
	}
	for k, v := range p.Env {
		if _, err := fmt.Fprintf(tmp, "%s=%s\n", k, v); err != nil {
			_ = tmp.Close()
			cleanup()
			return nil, fmt.Errorf("write: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return nil, fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		cleanup()
		return nil, fmt.Errorf("rename: %w", err)
	}
	return credsWriteResult{Path: final}, nil
}

type credsDeleteParams struct {
	DestID string `json:"dest_id"`
}

func backupDestCredsDeleteHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p credsDeleteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid_arg: %w", err)
	}
	if !validULID(p.DestID) {
		return nil, fmt.Errorf("invalid_arg: dest_id must be a 26-char ULID")
	}
	path := filepath.Join(credsDir, p.DestID+".env")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove %s: %w", path, err)
	}
	return map[string]string{"path": path, "status": "deleted"}, nil
}

func validULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'A' && r <= 'Z':
		default:
			return false
		}
	}
	return true
}

func validCredsKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func init() {
	Default.Register("backup.dest.creds_write", backupDestCredsWriteHandler)
	Default.Register("backup.dest.creds_delete", backupDestCredsDeleteHandler)
}
