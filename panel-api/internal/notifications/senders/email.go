package senders

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// smtpSendFunc is a test seam for net/smtp.SendMail. Production wires
// it to the stdlib implementation; tests inject a capturing fake so
// they don't need a real SMTP listener.
type smtpSendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// Email delivers via the local Stalwart submission port. Stalwart
// accepts unauthenticated submission from loopback (127.0.0.1) per
// ADR-0041, so the panel-api doesn't need SMTP credentials.
//
// Per-channel config drives the recipient + envelope sender; the body
// is rendered via RenderForChannel's "email" branch into a minimal
// HTML document.
type Email struct {
	addr string // "127.0.0.1:587" — Stalwart submission
	send smtpSendFunc
}

// NewEmail builds an Email sender that talks to Stalwart at the given
// address. An empty addr defaults to 127.0.0.1:587 which matches the
// install.sh Stalwart defaults.
func NewEmail(addr string) *Email {
	if addr == "" {
		addr = "127.0.0.1:587"
	}
	return &Email{addr: addr, send: smtp.SendMail}
}

func (e *Email) Kind() string { return models.NotificationChannelKindEmail }

// withSender is a test hook to swap the smtp.SendMail-compatible func.
func (e *Email) withSender(fn smtpSendFunc) *Email { e.send = fn; return e }

func (e *Email) Send(ctx context.Context, channel models.NotificationChannel, env notifications.Envelope) error {
	if channel.Config.ToEmail == "" {
		return fmt.Errorf("email: missing to_email in channel config: %w", notifications.ErrPermanent)
	}
	from := channel.Config.FromEmail
	if from == "" {
		return fmt.Errorf("email: missing from_email in channel config: %w", notifications.ErrPermanent)
	}
	// Reject addresses that contain CRLF — a malformed from/to could
	// inject extra headers into the message otherwise.
	for _, addr := range []string{from, channel.Config.ToEmail} {
		if strings.ContainsAny(addr, "\r\n") {
			return fmt.Errorf("email: invalid address (CRLF): %w", notifications.ErrPermanent)
		}
	}

	title, body := notifications.RenderForChannel(env, e.Kind())
	msg := buildMIME(from, channel.Config.ToEmail, title, body)

	// net/smtp doesn't honour ctx directly — but we respect cancellation
	// by bailing before the blocking call.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("email: context cancelled before send: %w", err)
	}
	if err := e.send(e.addr, nil, from, []string{channel.Config.ToEmail}, []byte(msg)); err != nil {
		// Stalwart rejections for bad addresses surface as 5xx perm
		// errors; net/smtp gives us a TextprotoError we stringify. We
		// treat all SMTP failures as transient here — Step 4 will add a
		// finer classifier if perm-vs-temp matters in practice.
		return fmt.Errorf("email: smtp send: %w", err)
	}
	return nil
}

func buildMIME(from, to, subject, htmlBody string) string {
	// Sanitise header inputs. Subject can contain unicode + commas, but
	// no CRLF; we also fold a plain-text subject so it transits as-is.
	subject = strings.ReplaceAll(subject, "\r", " ")
	subject = strings.ReplaceAll(subject, "\n", " ")

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	b.WriteString("\r\n")
	return b.String()
}
