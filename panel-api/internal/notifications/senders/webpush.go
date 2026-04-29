package senders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// WebPush delivers to every subscription row for env.UserID. VAPID keys
// live on server_settings (ADR-0057) — if they're missing (e.g. the
// EnsureVAPID seed hasn't run yet) Send returns ErrPermanent so the
// envelope lands in the DLQ rather than looping forever.
//
// 410 Gone from the browser push service means the subscription is
// retired; we delete the row and continue. Any other non-2xx response
// counts as transient.
// webpushSendFunc is a test seam mirroring webpush.SendNotificationWithContext.
// Production wires it to the real library; tests inject a capturing fake
// so they can exercise the 410-Gone path without crafting valid ECDH
// client keys.
type webpushSendFunc func(ctx context.Context, message []byte, s *webpush.Subscription, options *webpush.Options) (*http.Response, error)

type WebPush struct {
	settings repository.ServerSettingsRepository
	subs     repository.WebPushSubscriptionRepository
	log      *slog.Logger
	opts     webpush.Options
	send     webpushSendFunc
	// httpClient lets tests swap in httptest.NewServer for production
	// use of webpush-go's default. Only applied when send is the stock
	// library implementation.
	httpClient *http.Client
}

// NewWebPush constructs a WebPush sender. settings + subs are required;
// log may be nil (defaults to slog.Default()).
func NewWebPush(settings repository.ServerSettingsRepository, subs repository.WebPushSubscriptionRepository, log *slog.Logger) *WebPush {
	if log == nil {
		log = slog.Default()
	}
	return &WebPush{
		settings: settings,
		subs:     subs,
		log:      log,
		opts:     webpush.Options{TTL: 30},
		send:     webpush.SendNotificationWithContext,
	}
}

func (w *WebPush) Kind() string { return models.NotificationChannelKindWebpush }

// withClient is a test hook: replace the HTTP client webpush-go uses.
// Production does not need it; we hit the browser push service directly
// via DefaultClient.
func (w *WebPush) withClient(c *http.Client) *WebPush { w.httpClient = c; return w }

// withSender swaps the underlying SendNotification function. Tests use
// this to bypass ECE encryption and inject canned responses.
func (w *WebPush) withSender(fn webpushSendFunc) *WebPush { w.send = fn; return w }

type webPushPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Deeplink string `json:"deeplink,omitempty"`
	Severity string `json:"severity,omitempty"`
}

func (w *WebPush) Send(ctx context.Context, channel models.NotificationChannel, env notifications.Envelope) error {
	settings, err := w.settings.Get(ctx)
	if err != nil {
		return fmt.Errorf("webpush: load server_settings: %w", err)
	}
	if settings == nil || settings.VAPIDPublicKey == nil || settings.VAPIDPrivateKey == nil || *settings.VAPIDPublicKey == "" || *settings.VAPIDPrivateKey == "" {
		return fmt.Errorf("webpush: VAPID keys not seeded (did EnsureVAPID run?): %w", notifications.ErrPermanent)
	}
	subject := "mailto:admin@localhost"
	if settings.VAPIDSubject != nil && *settings.VAPIDSubject != "" {
		subject = *settings.VAPIDSubject
	}

	// Per-user envelope → push only to that admin's enrolled browsers.
	// Broadcast envelope (UserID empty: ssh.login, disk.full, etc.) →
	// fan out to every enrolled subscription so any admin who's opted
	// in hears about it. Subscription set is small (one row per
	// enrolled browser), so a full scan is cheaper than walking
	// admins-then-subs separately.
	var rows []models.WebPushSubscription
	if env.UserID != "" {
		rows, err = w.subs.FindByUser(ctx, env.UserID)
		if err != nil {
			return fmt.Errorf("webpush: list subs for %s: %w", env.UserID, err)
		}
	} else {
		rows, err = w.subs.FindAll(ctx)
		if err != nil {
			return fmt.Errorf("webpush: list all subs: %w", err)
		}
	}
	if len(rows) == 0 {
		// Nobody enrolled; not an error.
		return nil
	}

	title, body := notifications.RenderForChannel(env, w.Kind())
	payload, err := json.Marshal(webPushPayload{
		Title:    title,
		Body:     body,
		Deeplink: env.Deeplink,
		Severity: env.Severity,
	})
	if err != nil {
		return fmt.Errorf("webpush: marshal payload: %w", err)
	}

	opts := w.opts
	opts.Subscriber = subject
	opts.VAPIDPublicKey = *settings.VAPIDPublicKey
	opts.VAPIDPrivateKey = *settings.VAPIDPrivateKey
	if w.httpClient != nil {
		opts.HTTPClient = w.httpClient
	}

	var firstTransient error
	for _, row := range rows {
		sub := &webpush.Subscription{
			Endpoint: row.Endpoint,
			Keys: webpush.Keys{
				Auth:   row.Auth,
				P256dh: row.P256dh,
			},
		}
		resp, sendErr := w.send(ctx, payload, sub, &opts)
		if sendErr != nil {
			// Network-level failure for this sub. Note it, keep going —
			// other subs may still deliver.
			w.log.Warn("webpush: send failed", "endpoint_host", endpointHost(row.Endpoint), "err", sendErr)
			if firstTransient == nil {
				firstTransient = fmt.Errorf("webpush: %w", sendErr)
			}
			continue
		}
		func() {
			defer resp.Body.Close()
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, 512))
			switch {
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				_ = w.subs.TouchLastUsed(ctx, row.ID)
			case resp.StatusCode == http.StatusGone, resp.StatusCode == http.StatusNotFound:
				// Subscription retired by the browser. Delete + keep
				// processing — per-sub permanent, not per-envelope.
				if delErr := w.subs.DeleteByEndpoint(ctx, row.Endpoint); delErr != nil {
					w.log.Warn("webpush: delete retired sub failed", "endpoint_host", endpointHost(row.Endpoint), "err", delErr)
				} else {
					w.log.Info("webpush: sub retired, deleted", "endpoint_host", endpointHost(row.Endpoint), "status", resp.StatusCode)
				}
			default:
				if firstTransient == nil {
					firstTransient = fmt.Errorf("webpush: %d %s", resp.StatusCode, buf.String())
				}
				w.log.Warn("webpush: non-2xx", "endpoint_host", endpointHost(row.Endpoint), "status", resp.StatusCode)
			}
		}()
	}
	return firstTransient
}

// endpointHost extracts just the host from a push endpoint URL for
// logging — full endpoints include per-browser tokens we don't want in
// logs.
func endpointHost(url string) string {
	// Cheap parsing: scheme://HOST/... — return the middle slug.
	i := len("https://")
	if len(url) < i {
		return "?"
	}
	tail := url[i:]
	for j := 0; j < len(tail); j++ {
		if tail[j] == '/' {
			return tail[:j]
		}
	}
	return tail
}

