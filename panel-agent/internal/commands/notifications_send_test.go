package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestNotificationsSend_Allowlists(t *testing.T) {
	// Not t.Parallel — mutates package-level panelAPIClient + socket.
	good := notificationsSendParams{EventKind: "disk.full.warn", Severity: "warning", Title: "t", Body: "b"}
	require.NoError(t, validateNotificationParams(good))

	cases := []struct {
		name string
		mut  func(*notificationsSendParams)
	}{
		{"bad event_kind", func(p *notificationsSendParams) { p.EventKind = "not.in.list" }},
		{"bad severity", func(p *notificationsSendParams) { p.Severity = "meh" }},
		{"empty title", func(p *notificationsSendParams) { p.Title = "" }},
		{"title too long", func(p *notificationsSendParams) { p.Title = strings.Repeat("x", 201) }},
		{"body too long", func(p *notificationsSendParams) { p.Body = strings.Repeat("x", 2001) }},
		{"bad deeplink", func(p *notificationsSendParams) { p.Deeplink = "://" }},
	}
	for _, c := range cases {
		p := good
		c.mut(&p)
		err := validateNotificationParams(p)
		require.Error(t, err, c.name)
	}
}

func TestNotificationsSend_PostsToPanelSocket(t *testing.T) {
	// Stand up a unix-socket HTTP server that echoes an envelope id.
	dir := t.TempDir()
	sock := filepath.Join(dir, "api.sock")

	var gotBody []byte
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/internal/notifications/enqueue", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"1776998659731-0"}`))
	})
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// Point the package vars at the test socket.
	origPath := PanelAPISocketPath
	origClient := panelAPIClient
	PanelAPISocketPath = sock
	panelAPIClient = newPanelAPIClient()
	t.Cleanup(func() {
		PanelAPISocketPath = origPath
		panelAPIClient = origClient
	})

	params, _ := json.Marshal(notificationsSendParams{
		EventKind: "service.down",
		Severity:  "error",
		Title:     "jabali-panel.service is failing",
		Body:      "exited with status 1",
	})
	got, err := notificationsSendHandler(context.Background(), params)
	require.NoError(t, err)
	result, ok := got.(notificationsSendResult)
	require.True(t, ok)
	require.Equal(t, "1776998659731-0", result.ID)
	require.Equal(t, "/api/v1/internal/notifications/enqueue", gotPath)
	require.Contains(t, string(gotBody), `"event_kind":"service.down"`)
}

func TestNotificationsSend_PanelAPINon2xxIsInternal(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	origPath := PanelAPISocketPath
	origClient := panelAPIClient
	PanelAPISocketPath = sock
	panelAPIClient = newPanelAPIClient()
	t.Cleanup(func() {
		PanelAPISocketPath = origPath
		panelAPIClient = origClient
	})

	params, _ := json.Marshal(notificationsSendParams{EventKind: "service.down", Severity: "error", Title: "t", Body: "b"})
	_, err = notificationsSendHandler(context.Background(), params)
	require.Error(t, err)
	var ae *agentwire.AgentError
	require.True(t, errors.As(err, &ae))
	require.Equal(t, agentwire.CodeInternal, ae.Code)
}

func TestNotificationsSend_BadParamsBeforeDial(t *testing.T) {
	// Point socket at a path that does not exist — we should fail in
	// validation before the handler tries to dial.
	origPath := PanelAPISocketPath
	PanelAPISocketPath = "/tmp/jabali-nonexistent-" + os.Getenv("USER") + ".sock"
	t.Cleanup(func() { PanelAPISocketPath = origPath })

	params := json.RawMessage(`{"event_kind":"bogus","severity":"warning","title":"t","body":"b"}`)
	_, err := notificationsSendHandler(context.Background(), params)
	require.Error(t, err)
	var ae *agentwire.AgentError
	require.True(t, errors.As(err, &ae))
	require.Equal(t, agentwire.CodeInvalidArgument, ae.Code)
}
