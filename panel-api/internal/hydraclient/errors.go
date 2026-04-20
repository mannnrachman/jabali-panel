package hydraclient

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when Hydra answers 404 for a GET/DELETE.
// Typed so callers can branch on "client doesn't exist" vs "network
// glitch" — important for the apps-framework compensating transaction
// which treats a missing client-on-install-delete as already-clean,
// not a failure.
var ErrNotFound = errors.New("hydraclient: not found")

// ErrConflict is returned when Hydra answers 409 — typically a client
// with a duplicate client_id. Our apps framework generates ULIDs for
// client_id so this should never happen in practice, but a retry after
// a partial crash could hit it. Callers can idempotency-check by
// issuing a GetClient on conflict.
var ErrConflict = errors.New("hydraclient: conflict")

// ErrUnauthorized is returned when Hydra answers 401. Should never
// happen in practice since the admin URL is loopback-only; would
// indicate a misconfiguration (admin API exposed off-host and
// protected by a mutual-TLS or bearer that we're missing).
var ErrUnauthorized = errors.New("hydraclient: unauthorized")

// httpError wraps a non-2xx response with the status + body so
// callers get structured diagnostic context. Hydra's admin API
// returns JSON bodies shaped like:
//
//	{
//	  "error":             "string",
//	  "error_description": "string",
//	  "error_debug":       "string",
//	  "status_code":       number
//	}
//
// We surface the raw body (bounded to 512 bytes) because parsing the
// JSON adds dependency surface for no real callsite gain — the error
// text flows into slog lines and never into user-facing copy.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("hydraclient: HTTP %d: %s", e.Status, e.Body)
}

// mapStatusErr converts an HTTP status code into the closest typed
// sentinel error. Returns nil for 2xx. Use inside request helpers so
// every callsite gets the same 404→ErrNotFound mapping.
func mapStatusErr(status int, body string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	switch status {
	case 404:
		return ErrNotFound
	case 409:
		return ErrConflict
	case 401, 403:
		return ErrUnauthorized
	}
	return &httpError{Status: status, Body: truncateErrorBody(body)}
}

// truncateErrorBody caps a server-returned error body at 512 bytes so
// a misbehaving upstream can't flood our logs. The 512-byte limit is
// enough for Hydra's largest error JSON (error_debug with a full
// stack can hit ~400 bytes).
func truncateErrorBody(s string) string {
	const maxLen = 512
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}
