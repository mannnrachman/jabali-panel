// Package senders hosts the concrete ChannelSender implementations for
// M14 (ADR-0056). Each file corresponds to one channel kind; the
// dispatcher looks them up by kind via the notifications.Registry.
//
// Senders share a single *http.Client with a 10s timeout. Retries are
// the dispatcher's job — if a transport returns a transient error
// (non-nil err that isn't wrapped with notifications.ErrPermanent), the
// stream entry stays in the PEL for reclaim. Permanent errors (4xx on
// admin-configured URLs, malformed config, webpush 410 Gone on a single
// sub which we swallow after deleting the row) return ErrPermanent so
// the envelope doesn't loop forever.
package senders

import (
	"net/http"
	"time"
)

// DefaultHTTPTimeout bounds outbound senders. Admin-configured URLs
// point at third parties we don't control, so failures should surface
// fast rather than holding the dispatcher hostage.
const DefaultHTTPTimeout = 10 * time.Second

// newHTTPClient builds the shared *http.Client every HTTP-based sender
// uses. Exposed (lowercase, package-private) so tests can poke a
// transport via httptest.NewServer and still exercise the real
// request-shape code.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: DefaultHTTPTimeout}
}
