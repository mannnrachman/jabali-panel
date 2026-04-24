package eventsources

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// --- fakes ---

type capturingPublisher struct {
	mu       sync.Mutex
	envelopes []notifications.Envelope
}

func (c *capturingPublisher) Publish(_ context.Context, env notifications.Envelope) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.envelopes = append(c.envelopes, env)
	return "1-0", nil
}

func (c *capturingPublisher) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.envelopes)
}

func (c *capturingPublisher) Last() notifications.Envelope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.envelopes[len(c.envelopes)-1]
}

type fakeHistory struct {
	mu   sync.Mutex
	rows []models.NotificationHistory
}

func (f *fakeHistory) ListRecentByEvent(ctx context.Context, kind string, since time.Time) ([]models.NotificationHistory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []models.NotificationHistory
	for _, r := range f.rows {
		if r.EventKind == kind && !r.CreatedAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeHistory) recordFired(kind, body string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, models.NotificationHistory{
		EventKind: kind,
		Body:      body,
		CreatedAt: at,
	})
}

type fakeSSLCerts struct {
	rows []repository.SSLCertificateWithDomain
}

func (f *fakeSSLCerts) ListAll(context.Context) ([]repository.SSLCertificateWithDomain, error) {
	return f.rows, nil
}

// --- tests ---

func tsPtr(t time.Time) *time.Time { return &t }
func strPtr(s string) *string      { return &s }

func fixedNow() time.Time { return time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC) }

func TestCertRenew_Fires7d(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	pub := &capturingPublisher{}
	hist := &fakeHistory{}
	certs := &fakeSSLCerts{rows: []repository.SSLCertificateWithDomain{
		{ID: "c1", DomainName: "example.com", Status: "active", ExpiresAt: tsPtr(now.Add(5 * 24 * time.Hour))},
	}}
	d := Deps{Queue: pub, History: hist, SSLCerts: certs, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	certRenewPass(context.Background(), d)
	require.Equal(t, 1, pub.Count())
	require.Equal(t, "domain.expiry.7d", pub.Last().EventKind)
}

func TestCertRenew_Fires1d(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	pub := &capturingPublisher{}
	hist := &fakeHistory{}
	certs := &fakeSSLCerts{rows: []repository.SSLCertificateWithDomain{
		{ID: "c1", DomainName: "example.com", Status: "active", ExpiresAt: tsPtr(now.Add(12 * time.Hour))},
	}}
	d := Deps{Queue: pub, History: hist, SSLCerts: certs, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	certRenewPass(context.Background(), d)
	require.Equal(t, 1, pub.Count())
	require.Equal(t, "domain.expiry.1d", pub.Last().EventKind)
}

func TestCertRenew_Deduped(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	pub := &capturingPublisher{}
	hist := &fakeHistory{}
	// Simulate a prior fire for the same cert within the cooldown.
	hist.recordFired("domain.expiry.7d", "Certificate c1 for example.com expires ... (cert:c1)", now.Add(-time.Hour))
	certs := &fakeSSLCerts{rows: []repository.SSLCertificateWithDomain{
		{ID: "c1", DomainName: "example.com", Status: "active", ExpiresAt: tsPtr(now.Add(5 * 24 * time.Hour))},
	}}
	d := Deps{Queue: pub, History: hist, SSLCerts: certs, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	certRenewPass(context.Background(), d)
	require.Zero(t, pub.Count(), "should not fire within cooldown")
}

func TestCertRenew_FiresRenewFail(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	pub := &capturingPublisher{}
	hist := &fakeHistory{}
	certs := &fakeSSLCerts{rows: []repository.SSLCertificateWithDomain{
		{ID: "c1", DomainName: "example.com", Status: "failed", LastError: strPtr("acme: 429 rate limited"), ExpiresAt: tsPtr(now.Add(60 * 24 * time.Hour))},
	}}
	d := Deps{Queue: pub, History: hist, SSLCerts: certs, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	certRenewPass(context.Background(), d)
	require.Equal(t, 1, pub.Count())
	require.Equal(t, "cert.renew.fail", pub.Last().EventKind)
	require.Contains(t, pub.Last().Body, "429 rate limited")
}

func TestCertRenew_NoFireWellOutsideWindow(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	pub := &capturingPublisher{}
	hist := &fakeHistory{}
	certs := &fakeSSLCerts{rows: []repository.SSLCertificateWithDomain{
		{ID: "c1", DomainName: "example.com", Status: "active", ExpiresAt: tsPtr(now.Add(60 * 24 * time.Hour))},
	}}
	d := Deps{Queue: pub, History: hist, SSLCerts: certs, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	certRenewPass(context.Background(), d)
	require.Zero(t, pub.Count())
}
