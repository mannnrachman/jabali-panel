package notifications

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderForChannel_Markdown(t *testing.T) {
	t.Parallel()
	env := Envelope{Title: "Cert renewal failed", Body: "Let's Encrypt 429", Deeplink: "https://panel/admin/ssl"}
	for _, kind := range []string{"slack", "discord"} {
		title, body := RenderForChannel(env, kind)
		require.Equal(t, env.Title, title, kind)
		require.Contains(t, body, "Let's Encrypt 429", kind)
		require.Contains(t, body, "<https://panel/admin/ssl|View in Jabali>", kind)
	}
}

func TestRenderForChannel_NtfyPlain(t *testing.T) {
	t.Parallel()
	env := Envelope{Title: "Disk full", Body: "85%", Deeplink: "https://panel/disks"}
	title, body := RenderForChannel(env, "ntfy")
	require.Equal(t, "Disk full", title)
	require.Contains(t, body, "85%")
	require.Contains(t, body, "https://panel/disks")
	require.NotContains(t, body, "<") // plain text only
}

func TestRenderForChannel_WebPushTruncates(t *testing.T) {
	t.Parallel()
	longTitle := strings.Repeat("x", 200)
	longBody := strings.Repeat("y", 500)
	env := Envelope{Title: longTitle, Body: longBody}
	title, body := RenderForChannel(env, "webpush")
	// truncate pads "…" (3 bytes UTF-8) at the end when trimming.
	require.LessOrEqual(t, len([]rune(title)), 100)
	require.LessOrEqual(t, len([]rune(body)), 300)
}

func TestRenderForChannel_EmailHTML(t *testing.T) {
	t.Parallel()
	env := Envelope{Title: "t", Body: "line1\nline2", Severity: "error", Deeplink: "https://panel/"}
	title, body := RenderForChannel(env, "email")
	require.Equal(t, "t", title)
	require.Contains(t, body, "<p>line1<br>line2</p>")
	require.Contains(t, body, "Severity: error")
	require.Contains(t, body, `href="https://panel/"`)
}

func TestRenderForChannel_UnknownFallsBack(t *testing.T) {
	t.Parallel()
	env := Envelope{Title: "t", Body: "b"}
	title, body := RenderForChannel(env, "carrier-pigeon")
	require.Equal(t, env.Title, title)
	require.Equal(t, env.Body, body)
}
