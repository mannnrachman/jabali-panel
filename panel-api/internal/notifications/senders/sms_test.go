package senders

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

func TestSMS_PostsToAndBody(t *testing.T) {
	t.Parallel()
	var gotBody, gotSig, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotSig = r.Header.Get("X-Jabali-Signature")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	s := NewSMS()
	s.now = func() time.Time { return time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC) }
	ch := models.NotificationChannel{Kind: "sms", Config: models.NotificationChannelConfig{
		URL:        ts.URL,
		ToNumber:   "+15551234567",
		HMACSecret: "supersecretvalue",
		Bearer:     "tok-123",
	}}
	env := notifications.Envelope{EventKind: "service.down", Severity: "critical", Title: "panel-api down", Body: "5 restarts in 60s"}
	require.NoError(t, s.Send(context.Background(), ch, env))

	require.Contains(t, gotBody, `"to":"+15551234567"`)
	require.Contains(t, gotBody, `"body":"panel-api down: 5 restarts in 60s"`)
	require.Contains(t, gotBody, `"event_kind":"service.down"`)
	require.NotEmpty(t, gotSig)
	require.Equal(t, "Bearer tok-123", gotAuth)
}

func TestSMS_MissingConfigIsPermanent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewSMS()
	// Missing URL.
	require.True(t, errors.Is(s.Send(ctx, models.NotificationChannel{Kind: "sms"}, notifications.Envelope{Title: "x"}), notifications.ErrPermanent))
	// Missing ToNumber.
	require.True(t, errors.Is(s.Send(ctx, models.NotificationChannel{Kind: "sms", Config: models.NotificationChannelConfig{URL: "https://x"}}, notifications.Envelope{Title: "x"}), notifications.ErrPermanent))
}

func TestSMS_4xxPermanent(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer ts.Close()
	err := NewSMS().Send(context.Background(), models.NotificationChannel{Kind: "sms", Config: models.NotificationChannelConfig{URL: ts.URL, ToNumber: "+15550000000"}}, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}

func TestSMS_429Transient(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer ts.Close()
	err := NewSMS().Send(context.Background(), models.NotificationChannel{Kind: "sms", Config: models.NotificationChannelConfig{URL: ts.URL, ToNumber: "+15550000000"}}, notifications.Envelope{Title: "x"})
	require.Error(t, err)
	require.False(t, errors.Is(err, notifications.ErrPermanent))
}
