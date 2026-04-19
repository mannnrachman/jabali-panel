package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// serviceRestartParams names the service to restart. `name` is the bare
// unit basename (no `.service` suffix), matching what service.list
// returns — keeps round-trip symmetry obvious at the API layer.
type serviceRestartParams struct {
	Name string `json:"name"`
}

// serviceRestartResponse reports the post-restart state so the UI can
// re-render the status tag without a second round-trip.
type serviceRestartResponse struct {
	Name      string `json:"name"`
	Active    string `json:"active"`
	LoadState string `json:"load_state"`
}

// serviceRestartHandler is the inverse of service.list — takes one name
// from the same allow-list and hits `systemctl restart`. Refuses
// services we don't manage (security boundary) and refuses masked units
// (restarting a masked unit always fails — better to return a clean
// error than a systemctl stderr dump).
func serviceRestartHandler(ctx context.Context, params json.RawMessage) (any, error) {
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

	// Allow-list check: only services we publish via service.list may
	// be restarted. Prevents a compromised panel token from turning
	// the restart endpoint into arbitrary systemctl access.
	if !isAllowedService(p.Name) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("service %q not in allow-list", p.Name),
		}
	}

	// Validate characters too (belt-and-braces against injection).
	for _, c := range p.Name {
		if !isServiceNameChar(c) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: "invalid service name",
			}
		}
	}

	unit := fmt.Sprintf("%s.service", p.Name)

	// Bail early on masked units — systemctl restart on a masked unit
	// fails with "Unit X is masked", which is a surprising error to
	// surface in a UI toast. Report it as FailedPrecondition so the
	// API layer can render a helpful message.
	loadState, _ := systemctlRunner(ctx, "show", "-p", "LoadState", "--value", unit)
	if strings.TrimSpace(loadState) == "masked" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("%s is masked; unmask via systemctl before restarting", p.Name),
		}
	}
	if strings.TrimSpace(loadState) == "not-found" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("%s is not installed", p.Name),
		}
	}

	if out, err := systemctlRunner(ctx, "restart", unit); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl restart %s failed: %s", unit, strings.TrimSpace(out)),
		}
	}

	// Read the post-restart state so the UI can render immediately.
	post := probeService(ctx, p.Name)
	return serviceRestartResponse{
		Name:      post.Name,
		Active:    post.Active,
		LoadState: post.LoadState,
	}, nil
}

// isAllowedService reports whether name is in the combined allow-list.
// Kept local so the allow-list stays authoritative in service_list.go.
func isAllowedService(name string) bool {
	for _, s := range AllowedServices() {
		if s == name {
			return true
		}
	}
	return false
}

func init() {
	Default.Register("service.restart", serviceRestartHandler)
}
