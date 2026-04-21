// service_lifecycle.go — agent handlers for service.{stop, start, enable,
// disable}. The restart handler lives next door in service_restart.go for
// historical reasons; the four lifecycle verbs here share the same
// allow-list + character-validation + masked-check pattern.
//
// Security posture matches service.restart: the allow-list published by
// service.list is authoritative. A compromised panel token cannot turn
// any of these endpoints into arbitrary systemctl access. The
// panel-self-destruct guard (reject stop/disable for jabali-panel and
// jabali-agent) lives at the API layer — that's a product-UX concern,
// not a security boundary, and the agent stays obedient to whatever the
// allow-list says it can touch.

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// serviceActionParams is the shared input shape for stop/start/enable/disable.
type serviceActionParams struct {
	Name string `json:"name"`
}

// serviceActionResponse reports the post-action state so the UI can
// re-render immediately without a second round-trip.
type serviceActionResponse struct {
	Name      string `json:"name"`
	Active    string `json:"active"`
	LoadState string `json:"load_state"`
	Enabled   string `json:"enabled"`
}

// validateServiceAction parses params, runs the allow-list + character-
// validation + masked + not-found checks, and returns the validated name
// on success. Shared by all four lifecycle handlers.
//
// `rejectMasked` controls whether a masked unit is a valid target:
//   - stop/start/restart: true (systemctl verb fails on masked)
//   - enable: true (masked units can't be enabled without unmasking)
//   - disable: true (disabling a masked unit is a no-op + warning)
//
// All four happen to reject masked, so the flag is currently redundant —
// keeping the parameter makes future "unmask" or diagnostic endpoints
// explicit about their choice.
func validateServiceAction(ctx context.Context, params json.RawMessage, rejectMasked bool) (string, *agentwire.AgentError) {
	if len(params) == 0 {
		return "", &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "params required",
		}
	}
	var p serviceActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("parse params: %v", err),
		}
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return "", &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "name required",
		}
	}
	if !isAllowedService(name) {
		return "", &agentwire.AgentError{
			Code:    agentwire.CodePermissionDenied,
			Message: fmt.Sprintf("service %q not in allow-list", name),
		}
	}
	for _, c := range name {
		if !isServiceNameChar(c) {
			return "", &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: "invalid service name",
			}
		}
	}

	unit := fmt.Sprintf("%s.service", name)
	loadState, _ := systemctlRunner(ctx, "show", "-p", "LoadState", "--value", unit)
	loadState = strings.TrimSpace(loadState)

	if loadState == "not-found" {
		return "", &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("%s is not installed", name),
		}
	}
	if rejectMasked && loadState == "masked" {
		return "", &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("%s is masked; unmask via systemctl before acting on it", name),
		}
	}

	return name, nil
}

// runServiceAction applies the systemctl verb and returns the post-state.
// The verb must be a literal string the caller controls — not user input.
func runServiceAction(ctx context.Context, verb, name string) (any, error) {
	unit := fmt.Sprintf("%s.service", name)
	if out, err := systemctlRunner(ctx, verb, unit); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl %s %s failed: %s", verb, unit, strings.TrimSpace(out)),
		}
	}
	post := probeService(ctx, name)
	return serviceActionResponse{
		Name:      post.Name,
		Active:    post.Active,
		LoadState: post.LoadState,
		Enabled:   post.Enabled,
	}, nil
}

func serviceStopHandler(ctx context.Context, params json.RawMessage) (any, error) {
	name, ae := validateServiceAction(ctx, params, true)
	if ae != nil {
		return nil, ae
	}
	return runServiceAction(ctx, "stop", name)
}

func serviceStartHandler(ctx context.Context, params json.RawMessage) (any, error) {
	name, ae := validateServiceAction(ctx, params, true)
	if ae != nil {
		return nil, ae
	}
	return runServiceAction(ctx, "start", name)
}

func serviceEnableHandler(ctx context.Context, params json.RawMessage) (any, error) {
	name, ae := validateServiceAction(ctx, params, true)
	if ae != nil {
		return nil, ae
	}
	return runServiceAction(ctx, "enable", name)
}

func serviceDisableHandler(ctx context.Context, params json.RawMessage) (any, error) {
	name, ae := validateServiceAction(ctx, params, true)
	if ae != nil {
		return nil, ae
	}
	return runServiceAction(ctx, "disable", name)
}

func init() {
	Default.Register("service.stop", serviceStopHandler)
	Default.Register("service.start", serviceStartHandler)
	Default.Register("service.enable", serviceEnableHandler)
	Default.Register("service.disable", serviceDisableHandler)
}
