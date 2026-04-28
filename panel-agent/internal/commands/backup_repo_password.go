// M30.1 follow-up — agent-side reveal of /etc/jabali-panel/restic-repo.password.
// panel-api runs as the jabali user with ProtectSystem=strict and
// cannot read 0600 root:root files under /etc. Admin UI calls this
// agent command to surface the master encryption key for off-host
// backup (per ADR-0075 operator responsibility).
//
// SECURITY: command requires no params and returns the password
// verbatim. Access control sits in panel-api (RequireAdmin); the
// agent socket is already group-restricted to jabali-sockets so any
// caller able to reach it is already trusted.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const resticRepoPasswordPath = "/etc/jabali-panel/restic-repo.password"

type backupRepoPasswordResult struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

func backupRepoPasswordReadHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	body, err := os.ReadFile(resticRepoPasswordPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", resticRepoPasswordPath, err)
	}
	return backupRepoPasswordResult{
		Path:     resticRepoPasswordPath,
		Password: strings.TrimRight(string(body), "\n\r"),
	}, nil
}

func init() {
	Default.Register("backup.repo.password.read", backupRepoPasswordReadHandler)
}
