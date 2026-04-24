package senders

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

func TestSlack_PostsPayload(t *testing.T) {
	t.Parallel()

	var gotCT string
	var gotBody slackPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	s := NewSlack()
	ch := models.NotificationChannel{
		Kind:   "slack",
		Config: models.NotificationChannelConfig{URL: ts.URL},
	}
	env := notifications.Envelope{EventKind: "cert.renew.failure", Severity: "error", Title: "Renewal failed", Body: "Let's Encrypt 429"}
	require.NoError(t, s.Send(context.Background(), ch, env))
	require.Equal(t, "application/json", gotCT)
	require.Contains(t, gotBody.Text, "Renewal failed")
	require.Contains(t, gotBody.Text, "429")
}

func TestSlack_Missing4xxIsPermanent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "webhook revoked")
	}))
	defer ts.Close()

	s := NewSlack()
	ch := models.NotificationChannel{Kind: "slack", Config: models.NotificationChannelConfig{URL: ts.URL}}
	err := s.Send(context.Background(), ch, notifications.Envelope{Title: "x"})
	require.Error(t, err)
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}

func TestSlack_5xxIsTransient(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	s := NewSlack()
	ch := models.NotificationChannel{Kind: "slack", Config: models.NotificationChannelConfig{URL: ts.URL}}
	err := s.Send(context.Background(), ch, notifications.Envelope{Title: "x"})
	require.Error(t, err)
	require.False(t, errors.Is(err, notifications.ErrPermanent))
}

func TestSlack_MissingURL(t *testing.T) {
	t.Parallel()
	s := NewSlack()
	err := s.Send(context.Background(), models.NotificationChannel{Kind: "slack"}, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}
