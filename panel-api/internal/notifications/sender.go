package notifications

import (
	"context"
	"errors"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// ChannelSender is the plug-point every concrete delivery transport
// implements (email, Slack, ntfy, webhook, webpush — added in Step 3).
// The dispatcher looks up the sender for a channel by kind; unknown
// kinds surface as ErrUnknownKind and route straight to the DLQ.
type ChannelSender interface {
	// Kind returns the lower-case identifier that matches the
	// notification_channels.kind ENUM (e.g. "slack").
	Kind() string

	// Send delivers one envelope to one configured channel. Returning
	// nil marks the delivery as sent. Returning ErrPermanent (or any
	// error wrapping it) marks the history row permanently failed —
	// the dispatcher skips retries and routes straight to DLQ. Any
	// other error is treated as transient; the dispatcher leaves the
	// stream entry in the pending-entry-list for reclaim.
	Send(ctx context.Context, channel models.NotificationChannel, env Envelope) error
}

// ErrPermanent wraps sender errors the dispatcher must not retry.
// Common cases: 401/403/404 on a webhook URL, malformed config, a
// Web Push endpoint that returned 410 Gone.
var ErrPermanent = errors.New("notifications: permanent delivery failure")

// ErrUnknownKind is returned by Registry.Lookup for an unregistered
// channel kind. Surfaced by the dispatcher so the envelope goes to
// DLQ with a clear reason rather than silently stalling the consumer.
var ErrUnknownKind = errors.New("notifications: unknown channel kind")

// Registry is the goroutine-safe lookup table the dispatcher uses to
// resolve a channel to its sender. Registration happens at boot
// (serve.go); the map is read-mostly thereafter.
type Registry struct {
	mu      sync.RWMutex
	senders map[string]ChannelSender
}

func NewRegistry() *Registry { return &Registry{senders: map[string]ChannelSender{}} }

// Register adds a sender. A duplicate kind overwrites — callers should
// only register once per kind at boot; the overwrite semantics exist
// for test harnesses that reinitialise in a single process.
func (r *Registry) Register(s ChannelSender) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.senders[s.Kind()] = s
}

// Lookup returns the sender for a channel kind or ErrUnknownKind if
// none is registered.
func (r *Registry) Lookup(kind string) (ChannelSender, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.senders[kind]
	if !ok {
		return nil, ErrUnknownKind
	}
	return s, nil
}

// Kinds returns every registered kind. Used by the dispatcher to
// refuse to start if zero senders are wired in (a configuration bug
// better caught at boot than at first-event time).
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.senders))
	for k := range r.senders {
		out = append(out, k)
	}
	return out
}
