package senders

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// smtpSendFunc is a test seam for net/smtp.SendMail. Production wires
// it to the stdlib implementation; tests inject a capturing fake so
// they don't need a real SMTP listener.
type smtpSendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// implicitTLSSendFunc is a second test seam used only when the channel
// config selects SMTPTLS="tls" (implicit-TLS, RFC 8314, classically
// port 465). The signature matches smtpSendFunc for symmetry — the
// only difference is that the production binding wraps net.Dial in a
// tls.Client handshake before any SMTP traffic.
type implicitTLSSendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// Email delivers via either the local Stalwart submission port (the
// default — ADR-0041) or an externally-configured SMTP relay. Per-row
// config drives the recipient + envelope sender; the body is rendered
// via RenderForChannel's "email" branch into a minimal HTML document.
type Email struct {
	addr     string // local Stalwart fallback when channel.Config.SMTPMode is empty/"local"
	send     smtpSendFunc
	sendTLS  implicitTLSSendFunc
}

// NewEmail builds an Email sender that talks to Stalwart at the given
// address. An empty addr defaults to 127.0.0.1:587 which matches the
// install.sh Stalwart defaults.
func NewEmail(addr string) *Email {
	if addr == "" {
		addr = "127.0.0.1:587"
	}
	return &Email{addr: addr, send: smtp.SendMail, sendTLS: sendImplicitTLS}
}

func (e *Email) Kind() string { return models.NotificationChannelKindEmail }

// withSender is a test hook to swap the smtp.SendMail-compatible func.
func (e *Email) withSender(fn smtpSendFunc) *Email { e.send = fn; return e }

// withTLSSender is a test hook to swap the implicit-TLS path.
func (e *Email) withTLSSender(fn implicitTLSSendFunc) *Email { e.sendTLS = fn; return e }

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

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("email: context cancelled before send: %w", err)
	}

	addr, auth, useImplicitTLS, err := resolveTransport(e.addr, channel.Config)
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}

	var sendErr error
	if useImplicitTLS {
		sendErr = e.sendTLS(addr, auth, from, []string{channel.Config.ToEmail}, []byte(msg))
	} else {
		sendErr = e.send(addr, auth, from, []string{channel.Config.ToEmail}, []byte(msg))
	}
	if err := sendErr; err != nil {
		// Stalwart and well-behaved relays surface 5xx perm errors via a
		// TextprotoError we stringify. We treat all SMTP failures as
		// transient here — the dispatcher's retry policy decides.
		return fmt.Errorf("email: smtp send: %w", err)
	}
	return nil
}

// resolveTransport picks the dial address, auth, and TLS mode based on
// the channel config. Empty SMTPMode (or "local") falls back to the
// loopback Stalwart submission port; "smtp" routes to the configured
// external relay. Returns useImplicitTLS=true only when SMTPTLS="tls".
func resolveTransport(localAddr string, cfg models.NotificationChannelConfig) (string, smtp.Auth, bool, error) {
	switch cfg.SMTPMode {
	case "", "local":
		return localAddr, nil, false, nil
	case "smtp":
		if cfg.SMTPHost == "" {
			return "", nil, false, errors.New("smtp_host required when smtp_mode=smtp")
		}
		if cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
			return "", nil, false, fmt.Errorf("invalid smtp_port %d (must be 1–65535)", cfg.SMTPPort)
		}
		addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(cfg.SMTPPort))
		var auth smtp.Auth
		if cfg.SMTPUsername != "" || cfg.SMTPPassword != "" {
			auth = smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
		}
		switch cfg.SMTPTLS {
		case "", "starttls", "none":
			// stdlib smtp.SendMail issues STARTTLS opportunistically and
			// falls through to plaintext when the server doesn't offer
			// it; "none" callers explicitly accept that.
			return addr, auth, false, nil
		case "tls":
			return addr, auth, true, nil
		default:
			return "", nil, false, fmt.Errorf("invalid smtp_tls %q", cfg.SMTPTLS)
		}
	default:
		return "", nil, false, fmt.Errorf("unknown smtp_mode %q", cfg.SMTPMode)
	}
}

// sendImplicitTLS dials the SMTP relay over TLS (RFC 8314 / port 465
// pattern) and runs the standard SMTP exchange. Used when the channel
// config sets SMTPTLS="tls". Mirrors the smtp.SendMail signature so it
// can plug into the same Email.send slot.
func sendImplicitTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("split host: %w", err)
	}
	tlsCfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer c.Close()
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp RCPT: %w", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return c.Quit()
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
