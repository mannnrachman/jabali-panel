package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// app_dispatch.go provides the M19 generic agent commands —
// app.install, app.delete, app.clone — that read an `app_type`
// discriminator from the request body and forward the unchanged body
// to the matching app-specific handler.
//
// Each app-specific command file (e.g. wordpress_install.go) keeps its
// existing legacy registration on Default (so old wordpress.install
// callers don't break) AND opts into the dispatch table by calling
// RegisterAppInstaller / RegisterAppDeleter / RegisterAppCloner from
// its package-level init(). Adding a new app means: register on the
// legacy command name (optional), register on the app dispatch tables,
// and the panel's generic /applications routes light up automatically.

var (
	appDispatchMu sync.RWMutex
	appInstallers = map[string]Handler{}
	appDeleters   = map[string]Handler{}
	appCloners    = map[string]Handler{}
)

// RegisterAppInstaller adds h to the install dispatch table for
// appType. Panics on duplicate registration — every app declares its
// installer in exactly one init(), so a collision is a programmer
// error worth surfacing at startup, not a silent overwrite.
func RegisterAppInstaller(appType string, h Handler) {
	appDispatchMu.Lock()
	defer appDispatchMu.Unlock()
	if _, dup := appInstallers[appType]; dup {
		panic(fmt.Sprintf("agent: duplicate app installer registration %q", appType))
	}
	appInstallers[appType] = h
}

// RegisterAppDeleter mirrors RegisterAppInstaller for delete handlers.
func RegisterAppDeleter(appType string, h Handler) {
	appDispatchMu.Lock()
	defer appDispatchMu.Unlock()
	if _, dup := appDeleters[appType]; dup {
		panic(fmt.Sprintf("agent: duplicate app deleter registration %q", appType))
	}
	appDeleters[appType] = h
}

// RegisterAppCloner mirrors RegisterAppInstaller for clone handlers.
func RegisterAppCloner(appType string, h Handler) {
	appDispatchMu.Lock()
	defer appDispatchMu.Unlock()
	if _, dup := appCloners[appType]; dup {
		panic(fmt.Sprintf("agent: duplicate app cloner registration %q", appType))
	}
	appCloners[appType] = h
}

// appDispatchHead is the discriminator the dispatcher reads off every
// app.* request. The full body is forwarded unchanged — the per-app
// handler unmarshals into its own typed struct, app_type included or
// not (per-app structs ignore unknown fields).
type appDispatchHead struct {
	AppType string `json:"app_type"`
}

func dispatchByAppType(table map[string]Handler, op string, ctx context.Context, params json.RawMessage) (any, error) {
	var head appDispatchHead
	if err := json.Unmarshal(params, &head); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse app.%s params: %v", op, err),
		}
	}
	if head.AppType == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "app_type is required",
		}
	}
	appDispatchMu.RLock()
	h, ok := table[head.AppType]
	appDispatchMu.RUnlock()
	if !ok {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("unknown app_type %q for app.%s", head.AppType, op),
		}
	}
	return h(ctx, params)
}

func appInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	return dispatchByAppType(appInstallers, "install", ctx, params)
}

func appDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	return dispatchByAppType(appDeleters, "delete", ctx, params)
}

func appCloneHandler(ctx context.Context, params json.RawMessage) (any, error) {
	return dispatchByAppType(appCloners, "clone", ctx, params)
}

func init() {
	Default.Register("app.install", appInstallHandler)
	Default.Register("app.delete", appDeleteHandler)
	Default.Register("app.clone", appCloneHandler)
}
