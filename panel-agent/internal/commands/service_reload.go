package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// serviceReloadHandler is the no-downtime sibling of service.restart.
// Used for nginx, pdns, pdns-recursor where 'systemctl reload' picks
// up config changes without dropping in-flight connections. Same
// allow-list + masked-unit guards as service.restart.
//
// Services without a Reload= directive in the unit file will fail with
// systemd's "service does not support reload" error; the panel UI
// scopes Reload to a known-good list, so production callers won't hit
// that path. Surface unit-side rejections as Internal rather than
// FailedPrecondition because the systemctl reload exit code doesn't
// distinguish them.
func serviceReloadHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "params required",
		}
	}
	var p serviceRestartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("parse params: %v", err),
		}
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "name required",
		}
	}
	if !isAllowedService(p.Name) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("service %q not in allow-list", p.Name),
		}
	}
	for _, c := range p.Name {
		if !isServiceNameChar(c) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: "invalid service name",
			}
		}
	}

	unit := fmt.Sprintf("%s.service", p.Name)
	loadState, _ := systemctlRunner(ctx, "show", "-p", "LoadState", "--value", unit)
	if strings.TrimSpace(loadState) == "masked" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("%s is masked; unmask via systemctl before reloading", p.Name),
		}
	}
	if strings.TrimSpace(loadState) == "not-found" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("%s is not installed", p.Name),
		}
	}
	if out, err := systemctlRunner(ctx, "reload", unit); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl reload %s failed: %s", unit, strings.TrimSpace(out)),
		}
	}
	post := probeService(ctx, p.Name)
	return serviceRestartResponse{
		Name:      post.Name,
		Active:    post.Active,
		LoadState: post.LoadState,
	}, nil
}

func init() {
	Default.Register("service.reload", serviceReloadHandler)
}
