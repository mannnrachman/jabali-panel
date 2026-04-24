package senders

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

func TestEmail_SendsWellFormedMIME(t *testing.T) {
	t.Parallel()
	var gotFrom string
	var gotTo []string
	var gotMsg string
	fake := func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		gotFrom = from
		gotTo = to
		gotMsg = string(msg)
		return nil
	}
	e := NewEmail("").withSender(fake)
	ch := models.NotificationChannel{Kind: "email", Config: models.NotificationChannelConfig{ToEmail: "admin@example.com", FromEmail: "alerts@example.com"}}
	env := notifications.Envelope{Title: "cert.renew.fail", Body: "Let's Encrypt 429", Severity: "error"}
	require.NoError(t, e.Send(context.Background(), ch, env))

	require.Equal(t, "alerts@example.com", gotFrom)
	require.Equal(t, []string{"admin@example.com"}, gotTo)
	require.Contains(t, gotMsg, "Subject: cert.renew.fail")
	require.Contains(t, gotMsg, "Content-Type: text/html")
	require.Contains(t, gotMsg, "Let's Encrypt 429")
}

func TestEmail_MissingConfigPermanent(t *testing.T) {
	t.Parallel()
	e := NewEmail("")
	err := e.Send(context.Background(), models.NotificationChannel{Kind: "email"}, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
}

func TestEmail_RejectsHeaderInjection(t *testing.T) {
	t.Parallel()
	calls := 0
	fake := func(addr string, a smtp.Auth, from string, to []string, msg []byte) error { calls++; return nil }
	e := NewEmail("").withSender(fake)
	ch := models.NotificationChannel{Kind: "email", Config: models.NotificationChannelConfig{ToEmail: "a@b\r\nBcc: evil@x", FromEmail: "from@x"}}
	err := e.Send(context.Background(), ch, notifications.Envelope{Title: "x"})
	require.True(t, errors.Is(err, notifications.ErrPermanent))
	require.Zero(t, calls)
}

func TestEmail_StripsCRLFFromSubject(t *testing.T) {
	t.Parallel()
	var gotMsg string
	fake := func(addr string, a smtp.Auth, from string, to []string, msg []byte) error { gotMsg = string(msg); return nil }
	e := NewEmail("").withSender(fake)
	ch := models.NotificationChannel{Kind: "email", Config: models.NotificationChannelConfig{ToEmail: "a@b", FromEmail: "c@d"}}
	env := notifications.Envelope{Title: "line1\r\nBcc: attacker@x", Body: "ok", Severity: "info"}
	require.NoError(t, e.Send(context.Background(), ch, env))
	// CRLF in subject is folded to spaces — the important property is
	// that the Subject header stays on one line (no new header injected).
	headerBlock := gotMsg
	if i := strings.Index(gotMsg, "\r\n\r\n"); i >= 0 {
		headerBlock = gotMsg[:i]
	}
	var subjectLines []string
	for _, l := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(l, "Subject:") || strings.HasPrefix(l, "Bcc:") {
			subjectLines = append(subjectLines, l)
		}
	}
	require.Len(t, subjectLines, 1)
	require.True(t, strings.HasPrefix(subjectLines[0], "Subject:"))
}
