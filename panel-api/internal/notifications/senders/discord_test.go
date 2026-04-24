package senders

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

func TestDiscord_PostsEmbedAndColor(t *testing.T) {
	t.Parallel()

	var got discordPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	d := NewDiscord()
	ch := models.NotificationChannel{Kind: "discord", Config: models.NotificationChannelConfig{URL: ts.URL}}
	env := notifications.Envelope{Title: "disk.full", Body: "95%", Severity: models.NotificationSeverityWarning, Deeplink: "https://panel/admin"}
	require.NoError(t, d.Send(context.Background(), ch, env))
	require.Equal(t, "disk.full", got.Content)
	require.Len(t, got.Embeds, 1)
	require.Equal(t, 0xECB22E, got.Embeds[0].Color)
	require.Equal(t, "https://panel/admin", got.Embeds[0].URL)
}

func TestDiscord_4xxPermanent(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer ts.Close()
	err := NewDiscord().Send(context.Background(), models.NotificationChannel{Kind: "discord", Config: models.NotificationChannelConfig{URL: ts.URL}}, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}
