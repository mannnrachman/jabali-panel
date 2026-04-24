package senders

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

func TestNtfy_HeadersAndBody(t *testing.T) {
	t.Parallel()
	var gotHeaders http.Header
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	n := NewNtfy()
	ch := models.NotificationChannel{
		Kind: "ntfy",
		Config: models.NotificationChannelConfig{
			URL:      ts.URL,
			Bearer:   "abc",
			Priority: 4,
			Tags:     []string{"warning", "fire"},
		},
	}
	env := notifications.Envelope{Title: "Disk full", Body: "85%", Deeplink: "https://panel/disks"}
	require.NoError(t, n.Send(context.Background(), ch, env))

	require.Equal(t, "Disk full", gotHeaders.Get("Title"))
	require.Equal(t, "4", gotHeaders.Get("Priority"))
	require.Equal(t, "warning,fire", gotHeaders.Get("Tags"))
	require.Equal(t, "Bearer abc", gotHeaders.Get("Authorization"))
	require.Equal(t, "https://panel/disks", gotHeaders.Get("Click"))
	require.Contains(t, gotBody, "85%")
	require.Contains(t, gotBody, "https://panel/disks")
}

func TestNtfy_401Permanent(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer ts.Close()
	err := NewNtfy().Send(context.Background(), models.NotificationChannel{Kind: "ntfy", Config: models.NotificationChannelConfig{URL: ts.URL}}, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}
