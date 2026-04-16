// Package commands is the dispatch table that maps an incoming command
// string to its handler. One handler per file; registration happens via
// package init so new commands are a drop-in add.
//
// Handlers MUST be self-contained: input validation, any required
// subprocess invocations, and output shaping all live in the same file.
// That keeps the blast radius of a bad command contained to one place and
// makes it trivial to see the full contract of any privileged operation.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// Handler is the function every command implements. It gets a context that
// already carries the caller's deadline (if one was requested) and the raw
// params payload. It returns either a JSON-encoded data value, or an
// *agentwire.AgentError — any other error type is mapped to CodeInternal.
type Handler func(ctx context.Context, params json.RawMessage) (data any, err error)

// Registry holds the runtime command table. A zero-value Registry is usable.
// Concurrent Register + Dispatch calls are safe.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty registry. The package-level Default registry
// is what the binary uses in production; tests build their own so they
// don't poison the global.
func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

// Register attaches h to the given command name. Panics on collision —
// agent startup must fail loud rather than silently swap a command out.
func (r *Registry) Register(command string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = map[string]Handler{}
	}
	if _, dup := r.handlers[command]; dup {
		panic(fmt.Sprintf("agent: duplicate command registration %q", command))
	}
	r.handlers[command] = h
}

// Dispatch looks up command and runs it. Errors that aren't already typed
// AgentError become CodeInternal with their .Error() message — we never
// leak a raw error text to the wire without an explicit code.
func (r *Registry) Dispatch(ctx context.Context, command string, params json.RawMessage) (json.RawMessage, *agentwire.AgentError) {
	r.mu.RLock()
	h, ok := r.handlers[command]
	r.mu.RUnlock()
	if !ok {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeUnknownCommand,
			Message: fmt.Sprintf("no handler for command %q", command),
		}
	}
	data, err := h(ctx, params)
	if err != nil {
		var ae *agentwire.AgentError
		if errors.As(err, &ae) {
			return nil, ae
		}
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}
	if data == nil {
		return nil, nil
	}
	b, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("encode response: %v", marshalErr),
		}
	}
	return b, nil
}

// Commands returns the registered command names, sorted ascending so log
// output and tests don't depend on map iteration order.
func (r *Registry) Commands() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

// sortStrings is inlined here (rather than importing sort) so hot paths
// stay allocation-free. Commands() isn't hot; keeping it local for style.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// Default is the process-wide registry the agent binary uses. Command files
// register themselves into it via init(). Tests that want isolation should
// build their own Registry rather than touch Default.
var Default = NewRegistry()
