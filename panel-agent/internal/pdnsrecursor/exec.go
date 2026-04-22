package pdnsrecursor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"time"
)

// osExecRunner is the default ExecRunner. Each Run call is bounded by
// Timeout; a zero Timeout means no timeout (discouraged — rec_control
// has been observed to hang under memory pressure).
type osExecRunner struct {
	Timeout time.Duration
}

func (r *osExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %v: %w (stderr=%q)", name, args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// lookupUID / lookupGID are small stdlib wrappers so manager.go can chown
// without pulling os/user into its import list.
func lookupUID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

func lookupGID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}
