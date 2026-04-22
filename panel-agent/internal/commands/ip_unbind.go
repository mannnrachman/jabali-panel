package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type ipUnbindReq struct {
	Address   string `json:"address"`
	Prefixlen *int   `json:"prefixlen,omitempty"`
	Interface string `json:"interface,omitempty"`
}

type ipUnbindResp struct {
	Unbound bool `json:"unbound"`
}

// ipUnbindHandler is idempotent by design: `Cannot assign requested
// address` (ENOADDR) and `Cannot find device` map to success because
// both mean "the end state we want is already in place". Any other
// non-zero exit surfaces as an AgentError so the operator knows
// something actually blocked the removal.
func ipUnbindHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req ipUnbindReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "address required"}
	}
	ip := net.ParseIP(req.Address)
	if ip == nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "address not parseable"}
	}

	isV4 := strings.Contains(req.Address, ".") && !strings.Contains(req.Address, ":")
	prefix := 0
	switch {
	case req.Prefixlen != nil:
		prefix = *req.Prefixlen
	case isV4:
		prefix = 32
	default:
		prefix = 128
	}

	// Resolve the interface: if not supplied, look it up from ip.list —
	// matching the current location of the address. Idempotent if the
	// address isn't found.
	iface := req.Interface
	if iface == "" {
		entries, err := snapshotAddresses(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Address == req.Address {
				iface = e.Interface
				break
			}
		}
	}
	if iface == "" {
		// Already absent; treat as success.
		return ipUnbindResp{Unbound: true}, nil
	}

	cidr := fmt.Sprintf("%s/%d", req.Address, prefix)
	cmd := exec.CommandContext(ctx, "ip", "addr", "del", cidr, "dev", iface)
	stderr, err := runCaptureStderr(cmd)
	if err != nil {
		s := strings.ToLower(stderr)
		if strings.Contains(s, "cannot assign requested address") ||
			strings.Contains(s, "cannot find device") ||
			strings.Contains(s, "no such device") {
			return ipUnbindResp{Unbound: true}, nil
		}
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("ip addr del %s dev %s: %v (stderr: %s)", cidr, iface, err, stderr),
		}
	}
	return ipUnbindResp{Unbound: true}, nil
}

func init() {
	Default.Register("ip.unbind", ipUnbindHandler)
}
