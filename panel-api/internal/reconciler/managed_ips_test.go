package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// fakeManagedIPs is the in-memory ManagedIPRepository fake for
// reconciler tests. Mirrors the shape in api/ips_test.go so we don't
// grow two divergent mocks.
type fakeManagedIPs struct {
	rows    []models.ManagedIP
	updates []models.ManagedIP
}

func (f *fakeManagedIPs) Create(ctx context.Context, ip *models.ManagedIP) error { return nil }
func (f *fakeManagedIPs) Update(ctx context.Context, ip *models.ManagedIP) error {
	f.updates = append(f.updates, *ip)
	for i := range f.rows {
		if f.rows[i].ID == ip.ID {
			f.rows[i] = *ip
		}
	}
	return nil
}
func (f *fakeManagedIPs) Delete(ctx context.Context, id uint64) error { return nil }
func (f *fakeManagedIPs) FindByID(ctx context.Context, id uint64) (*models.ManagedIP, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			return &f.rows[i], nil
		}
	}
	return nil, repository.ErrNotFound
}
func (f *fakeManagedIPs) FindByAddress(ctx context.Context, addr string) (*models.ManagedIP, error) {
	return nil, repository.ErrNotFound
}
func (f *fakeManagedIPs) ListAll(ctx context.Context) ([]models.ManagedIP, error) {
	out := make([]models.ManagedIP, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
func (f *fakeManagedIPs) FindUnbound(ctx context.Context) ([]models.ManagedIP, error) {
	var out []models.ManagedIP
	for _, r := range f.rows {
		if !r.IsBound {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeManagedIPs) CountDomainsUsingIP(ctx context.Context, id uint64) (int64, error) {
	return 0, nil
}
func (f *fakeManagedIPs) FindDefaultByFamily(ctx context.Context, family string) (*models.ManagedIP, error) {
	for i := range f.rows {
		if f.rows[i].IsDefault && f.rows[i].Family == family {
			return &f.rows[i], nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeManagedIPs) EnsureDefault(ctx context.Context, address, family string) error {
	return nil
}

// stubAgent records calls and returns canned responses. We keep it in
// this _test.go file (rather than share the one in api/) because the
// reconciler package doesn't import api/.
type stubAgent struct {
	calls          []agentCall
	ipListResponse string
	bindShouldFail bool
}

type agentCall struct {
	Command string
	Params  any
}

func (s *stubAgent) Call(ctx context.Context, command string, params any) (json.RawMessage, error) {
	s.calls = append(s.calls, agentCall{Command: command, Params: params})
	switch command {
	case "ip.list":
		return json.RawMessage(s.ipListResponse), nil
	case "ip.bind":
		if s.bindShouldFail {
			return nil, errors.New("simulated bind failure")
		}
		return json.RawMessage(`{"bound":true,"reachable":true}`), nil
	}
	return nil, nil
}

// Verify we still satisfy the AgentInterface contract.
var _ agent.AgentInterface = (*stubAgent)(nil)

func newReconcilerWith(repo *fakeManagedIPs, a *stubAgent) *Reconciler {
	r := &Reconciler{
		managedIPs: repo,
		agent:      a,
		log:        slog.Default(),
	}
	return r
}

func TestReconcileManagedIPs_NoManagedIPsRepo_NoOp(t *testing.T) {
	r := &Reconciler{agent: &stubAgent{}, log: slog.Default()}
	r.ReconcileManagedIPs(context.Background())
	// Nothing to assert — just make sure it didn't panic on the nil repo path.
}

func TestReconcileManagedIPs_BoundAndPresent_NoBindCall(t *testing.T) {
	repo := &fakeManagedIPs{
		rows: []models.ManagedIP{
			{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsBound: true},
		},
	}
	a := &stubAgent{ipListResponse: `{"entries":[{"address":"203.0.113.1","family":"ipv4","interface":"eth0"}]}`}
	r := newReconcilerWith(repo, a)
	r.ReconcileManagedIPs(context.Background())

	// Exactly one agent call (ip.list); no ip.bind because address already present.
	if len(a.calls) != 1 || a.calls[0].Command != "ip.list" {
		t.Errorf("unexpected calls: %+v", a.calls)
	}
	if len(repo.updates) != 0 {
		t.Errorf("no row updates expected, got %d", len(repo.updates))
	}
}

func TestReconcileManagedIPs_BoundButMissing_CallsBind(t *testing.T) {
	repo := &fakeManagedIPs{
		rows: []models.ManagedIP{
			{ID: 1, Address: "203.0.113.99", Family: "ipv4", IsBound: true},
		},
	}
	a := &stubAgent{ipListResponse: `{"entries":[]}`}
	r := newReconcilerWith(repo, a)
	r.ReconcileManagedIPs(context.Background())

	if len(a.calls) != 2 {
		t.Fatalf("expected list+bind, got calls=%+v", a.calls)
	}
	if a.calls[1].Command != "ip.bind" {
		t.Errorf("expected ip.bind call after ip.list, got %s", a.calls[1].Command)
	}
}

func TestReconcileManagedIPs_BindFails_MarksDegraded(t *testing.T) {
	repo := &fakeManagedIPs{
		rows: []models.ManagedIP{
			{ID: 1, Address: "203.0.113.99", Family: "ipv4", IsBound: true, Degraded: false},
		},
	}
	a := &stubAgent{
		ipListResponse: `{"entries":[]}`,
		bindShouldFail: true,
	}
	r := newReconcilerWith(repo, a)
	r.ReconcileManagedIPs(context.Background())

	if len(repo.updates) != 1 || !repo.updates[0].Degraded {
		t.Errorf("expected Degraded flipped TRUE on update; got %+v", repo.updates)
	}
}

func TestReconcileManagedIPs_UnboundRowUntouched(t *testing.T) {
	repo := &fakeManagedIPs{
		rows: []models.ManagedIP{
			{ID: 1, Address: "203.0.113.5", Family: "ipv4", IsBound: false},
		},
	}
	a := &stubAgent{ipListResponse: `{"entries":[]}`}
	r := newReconcilerWith(repo, a)
	r.ReconcileManagedIPs(context.Background())

	for _, c := range a.calls {
		if c.Command == "ip.bind" {
			t.Fatalf("unbound row triggered ip.bind — should be skipped entirely")
		}
	}
}

func TestReconcileManagedIPs_ClearsDegradedWhenPresent(t *testing.T) {
	repo := &fakeManagedIPs{
		rows: []models.ManagedIP{
			{ID: 1, Address: "203.0.113.1", Family: "ipv4", IsBound: true, Degraded: true},
		},
	}
	a := &stubAgent{ipListResponse: `{"entries":[{"address":"203.0.113.1","family":"ipv4","interface":"eth0"}]}`}
	r := newReconcilerWith(repo, a)
	r.ReconcileManagedIPs(context.Background())

	if len(repo.updates) != 1 || repo.updates[0].Degraded {
		t.Errorf("expected Degraded cleared when address is back on kernel; got %+v", repo.updates)
	}
}
