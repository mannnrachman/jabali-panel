package senders

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

func TestWebhook_SignsBody(t *testing.T) {
	t.Parallel()
	secret := "shhh"

	var gotSig, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Jabali-Signature")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	wh := NewWebhook()
	wh.now = func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) }
	ch := models.NotificationChannel{Kind: "webhook", Config: models.NotificationChannelConfig{URL: ts.URL, HMACSecret: secret}}
	env := notifications.Envelope{EventKind: "cert.renew.fail", Severity: "error", Title: "t", Body: "b", UserID: "u"}
	require.NoError(t, wh.Send(context.Background(), ch, env))

	expect := hmac.New(sha256.New, []byte(secret))
	expect.Write([]byte(gotBody))
	want := "sha256=" + hex.EncodeToString(expect.Sum(nil))
	require.Equal(t, want, gotSig)
	require.Contains(t, gotBody, `"event_kind":"cert.renew.fail"`)
	require.Contains(t, gotBody, `"ts":"2026-04-24T12:00:00Z"`)
}

func TestWebhook_MissingConfigIsPermanent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	wh := NewWebhook()
	require.True(t, errors.Is(wh.Send(ctx, models.NotificationChannel{Kind: "webhook"}, notifications.Envelope{Title: "x"}), notifications.ErrPermanent))
	require.True(t, errors.Is(wh.Send(ctx, models.NotificationChannel{Kind: "webhook", Config: models.NotificationChannelConfig{URL: "https://x"}}, notifications.Envelope{Title: "x"}), notifications.ErrPermanent))
}

func TestWebhook_4xxPermanent(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer ts.Close()
	err := NewWebhook().Send(context.Background(), models.NotificationChannel{Kind: "webhook", Config: models.NotificationChannelConfig{URL: ts.URL, HMACSecret: "s"}}, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}

func TestWebhook_429Transient(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer ts.Close()
	err := NewWebhook().Send(context.Background(), models.NotificationChannel{Kind: "webhook", Config: models.NotificationChannelConfig{URL: ts.URL, HMACSecret: "s"}}, notifications.Envelope{Title: "x"})
	require.Error(t, err)
	require.False(t, errors.Is(err, notifications.ErrPermanent))
}
