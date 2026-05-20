package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// fakeMTAStsAgent records every call so tests can assert dispatch
// shape + count.
type fakeMTAStsAgent struct {
	calls   []fakeMTAStsCall
	failCmd map[string]error
}

type fakeMTAStsCall struct {
	Command string
	Params  map[string]any
}

func (f *fakeMTAStsAgent) Call(_ context.Context, cmd string, params any) (json.RawMessage, error) {
	m, _ := params.(map[string]any)
	f.calls = append(f.calls, fakeMTAStsCall{Command: cmd, Params: m})
	if err, ok := f.failCmd[cmd]; ok {
		return nil, err
	}
	return json.RawMessage(`{"ok":true}`), nil
}

func mtaStsReconcilerForTest(t *testing.T) (*Reconciler, *fakeDomainRepo, *fakeSSLCertRepo, *fakeServerSettingsRepo, *fakeMTAStsAgent) {
	t.Helper()
	ag := &fakeMTAStsAgent{}
	dr := newFakeDomainRepo()
	ss := &fakeServerSettingsRepo{settings: &models.ServerSettings{Hostname: "host.example.com"}}
	sc := newFakeSSLCertRepo()
	r := New(dr, nil, ag, slog.Default(), Config{}).WithSSLCerts(sc)
	r.serverSettings = ss
	return r, dr, sc, ss, ag
}

func TestReconcileMTASts_AppliesWhenIdChangedAndCertReady(t *testing.T) {
	r, dr, sc, _, ag := mtaStsReconcilerForTest(t)
	certPath := "/etc/letsencrypt/live/example.com/fullchain.pem"
	keyPath := "/etc/letsencrypt/live/example.com/privkey.pem"
	sc.byDomain["dom1"] = &models.SSLCertificate{
		ID: "ssl1", DomainID: "dom1", Status: models.SSLStatusIssued,
		CertPath: &certPath, KeyPath: &keyPath,
	}
	dom := &models.Domain{ID: "dom1", Name: "example.com", MTASTSEnabled: true, MTASTSId: 12345, MTASTSAppliedId: 0}
	dr.domains["dom1"] = dom

	r.reconcileMTAStsForDomain(context.Background(), dom)

	require.Len(t, ag.calls, 1, "exactly one apply expected")
	assert.Equal(t, "mail.mtasts.apply", ag.calls[0].Command)
	assert.Equal(t, "example.com", ag.calls[0].Params["domain"])
	assert.Equal(t, "host.example.com", ag.calls[0].Params["mx_host"])
	assert.Equal(t, "testing", ag.calls[0].Params["mode"])
	assert.Equal(t, certPath, ag.calls[0].Params["ssl_cert"])
	assert.Equal(t, uint64(12345), dr.domains["dom1"].MTASTSAppliedId, "applied_id should catch up to mta_sts_id")
}

func TestReconcileMTASts_NoOpWhenAlreadyApplied(t *testing.T) {
	r, dr, sc, _, ag := mtaStsReconcilerForTest(t)
	certPath := "/c.pem"; keyPath := "/k.pem"
	sc.byDomain["dom1"] = &models.SSLCertificate{
		ID: "ssl1", DomainID: "dom1", Status: models.SSLStatusIssued,
		CertPath: &certPath, KeyPath: &keyPath,
	}
	dom := &models.Domain{ID: "dom1", Name: "example.com", MTASTSEnabled: true, MTASTSId: 100, MTASTSAppliedId: 100}
	dr.domains["dom1"] = dom

	r.reconcileMTAStsForDomain(context.Background(), dom)

	assert.Empty(t, ag.calls, "no dispatch when applied_id == mta_sts_id")
}

func TestReconcileMTASts_RetainsAppliedIdOnAgentError(t *testing.T) {
	r, dr, sc, _, ag := mtaStsReconcilerForTest(t)
	ag.failCmd = map[string]error{"mail.mtasts.apply": errors.New("nginx -t failed: SAN not yet covered")}
	certPath := "/c.pem"; keyPath := "/k.pem"
	sc.byDomain["dom1"] = &models.SSLCertificate{
		ID: "ssl1", DomainID: "dom1", Status: models.SSLStatusIssued,
		CertPath: &certPath, KeyPath: &keyPath,
	}
	dom := &models.Domain{ID: "dom1", Name: "example.com", MTASTSEnabled: true, MTASTSId: 7, MTASTSAppliedId: 0}
	dr.domains["dom1"] = dom

	r.reconcileMTAStsForDomain(context.Background(), dom)

	require.Len(t, ag.calls, 1)
	assert.Equal(t, uint64(0), dr.domains["dom1"].MTASTSAppliedId, "applied_id must stay stale on error so next tick retries")
}

func TestReconcileMTASts_DisablesWhenFlagOffButPreviouslyApplied(t *testing.T) {
	r, dr, _, _, ag := mtaStsReconcilerForTest(t)
	dom := &models.Domain{ID: "dom1", Name: "example.com", MTASTSEnabled: false, MTASTSId: 7, MTASTSAppliedId: 7}
	dr.domains["dom1"] = dom

	r.reconcileMTAStsForDomain(context.Background(), dom)

	require.Len(t, ag.calls, 1)
	assert.Equal(t, "mail.mtasts.disable", ag.calls[0].Command)
	assert.Equal(t, uint64(0), dr.domains["dom1"].MTASTSAppliedId, "applied_id should clear after disable")
}

func TestReconcileMTASts_NoOpWhenDisabledAndNeverApplied(t *testing.T) {
	r, dr, _, _, ag := mtaStsReconcilerForTest(t)
	dom := &models.Domain{ID: "dom1", Name: "example.com", MTASTSEnabled: false, MTASTSAppliedId: 0}
	dr.domains["dom1"] = dom

	r.reconcileMTAStsForDomain(context.Background(), dom)

	assert.Empty(t, ag.calls)
}

func TestReconcileMTASts_NoOpWhenCertMissing(t *testing.T) {
	r, dr, _, _, ag := mtaStsReconcilerForTest(t)
	dom := &models.Domain{ID: "dom-no-cert", Name: "example.com", MTASTSEnabled: true, MTASTSId: 1, MTASTSAppliedId: 0}
	dr.domains["dom-no-cert"] = dom

	r.reconcileMTAStsForDomain(context.Background(), dom)

	assert.Empty(t, ag.calls, "no cert file → no apply (wait for SSL reconcile)")
}
