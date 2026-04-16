// Package agent is the panel-side client for the jabali-agent daemon. The
// wire-level types (Request, Response, AgentError) live in the top-level
// agentwire package so both this client and the panel-agent server can
// import them without tripping Go's internal/ rule.
//
// This file re-exports the wire types under the agent namespace so existing
// callers keep working and new code can pick whichever import feels more
// natural at the call site.
package agent

import (
	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// Request, Response, AgentError re-export the wire types.
type (
	Request    = agentwire.Request
	Response   = agentwire.Response
	AgentError = agentwire.AgentError
)

// Error codes re-exported from agentwire for convenience.
const (
	CodeInvalidArgument   = agentwire.CodeInvalidArgument
	CodeNotFound          = agentwire.CodeNotFound
	CodeAlreadyExists     = agentwire.CodeAlreadyExists
	CodePermissionDenied  = agentwire.CodePermissionDenied
	CodeUnavailable       = agentwire.CodeUnavailable
	CodeDeadlineExceeded  = agentwire.CodeDeadlineExceeded
	CodeInternal          = agentwire.CodeInternal
	CodeUnknownCommand    = agentwire.CodeUnknownCommand
	CodeMalformedEnvelope = agentwire.CodeMalformedEnvelope
)

// ErrMalformedResponse, ErrResponseIDMismatch also re-exported.
var (
	ErrMalformedResponse  = agentwire.ErrMalformedResponse
	ErrResponseIDMismatch = agentwire.ErrResponseIDMismatch
)
