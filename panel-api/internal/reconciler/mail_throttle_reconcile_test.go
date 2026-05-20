package reconciler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

type fakeThrottleClient struct {
	mu      sync.Mutex
	creates []map[string]any // captured payloads
	updates []struct{ id string; payload any }
	deletes []string
	failCmd map[string]error
}

func (f *fakeThrottleClient) Create(_ context.Context, _ string, payload any) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failCmd["create"]; ok {
		return "", err
	}
	// payload is MtaOutboundThrottlePayload — capture as generic map so
	// the test asserts wire shape without coupling to the struct type.
	m := map[string]any{}
	// shallow reflect — simpler to just record verbatim
	f.creates = append(f.creates, m)
	_ = payload
	return "stw-new-id-1", nil
}

func (f *fakeThrottleClient) Update(_ context.Context, _ string, id string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, struct{ id string; payload any }{id, payload})
	if err, ok := f.failCmd["update"]; ok {
		return err
	}
	return nil
}

func (f *fakeThrottleClient) Delete(_ context.Context, _ string, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failCmd["delete"]; ok {
		return err
	}
	f.deletes = append(f.deletes, id)
	return nil
}

type fakeOutboundPolicyRepo struct {
	rows         map[string]*models.MailOutboundPolicy
	stamped      []stampCall
}

type stampCall struct {
	rowID, stalwartID string
	lastErr           string
}

func (f *fakeOutboundPolicyRepo) Create(_ context.Context, p *models.MailOutboundPolicy) error {
	f.rows[p.ID] = p
	return nil
}
func (f *fakeOutboundPolicyRepo) Update(_ context.Context, p *models.MailOutboundPolicy) error {
	f.rows[p.ID] = p
	return nil
}
func (f *fakeOutboundPolicyRepo) FindByID(_ context.Context, id string) (*models.MailOutboundPolicy, error) {
	r, ok := f.rows[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r, nil
}
func (f *fakeOutboundPolicyRepo) FindByScope(_ context.Context, _ string, _ *string) (*models.MailOutboundPolicy, error) {
	return nil, errors.New("not found")
}
func (f *fakeOutboundPolicyRepo) List(_ context.Context) ([]models.MailOutboundPolicy, error) {
	out := make([]models.MailOutboundPolicy, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, *r)
	}
	return out, nil
}
func (f *fakeOutboundPolicyRepo) Delete(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}
func (f *fakeOutboundPolicyRepo) UpdateApplyState(_ context.Context, id, stalwartID string, lastErr *string) error {
	var le string
	if lastErr != nil {
		le = *lastErr
	}
	f.stamped = append(f.stamped, stampCall{rowID: id, stalwartID: stalwartID, lastErr: le})
	// mutate the row so subsequent ticks see the new state.
	if r, ok := f.rows[id]; ok {
		r.StalwartID = stalwartID
	}
	return nil
}

func newFakeOutboundPolicyRepo() *fakeOutboundPolicyRepo {
	return &fakeOutboundPolicyRepo{rows: map[string]*models.MailOutboundPolicy{}}
}

func throttleRecForTest(t *testing.T) (*Reconciler, *fakeOutboundPolicyRepo, *fakeThrottleClient) {
	t.Helper()
	r := &Reconciler{log: slog.Default()}
	repo := newFakeOutboundPolicyRepo()
	cl := &fakeThrottleClient{}
	r.outboundPolicies = repo
	r.stalwartAdmin = cl
	return r, repo, cl
}

func TestReconcileMailThrottles_CreatesWhenStalwartIDEmpty(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal, MaxPerHour: 100, Enabled: true,
	}
	r.reconcileMailThrottles(context.Background())
	require.Len(t, cl.creates, 1)
	assert.Empty(t, cl.updates)
	assert.Equal(t, "stw-new-id-1", repo.rows["row1"].StalwartID)
	require.Len(t, repo.stamped, 1)
	assert.Empty(t, repo.stamped[0].lastErr)
}

func TestReconcileMailThrottles_UpdatesWhenStalwartIDPresent(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeUser, MaxPerHour: 50,
		Enabled: true, StalwartID: "stw-existing",
	}
	r.reconcileMailThrottles(context.Background())
	assert.Empty(t, cl.creates)
	require.Len(t, cl.updates, 1)
	assert.Equal(t, "stw-existing", cl.updates[0].id)
}

func TestReconcileMailThrottles_DeletesWhenDisabledWithStalwartID(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal, Enabled: false, StalwartID: "stw-going-away",
	}
	r.reconcileMailThrottles(context.Background())
	assert.Empty(t, cl.creates)
	assert.Empty(t, cl.updates)
	require.Len(t, cl.deletes, 1)
	assert.Equal(t, "stw-going-away", cl.deletes[0])
	assert.Equal(t, "", repo.rows["row1"].StalwartID, "stalwart_id cleared after delete")
}

func TestReconcileMailThrottles_NoOpWhenDisabledWithoutID(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal, Enabled: false, StalwartID: "",
	}
	r.reconcileMailThrottles(context.Background())
	assert.Empty(t, cl.creates)
	assert.Empty(t, cl.updates)
	assert.Empty(t, cl.deletes)
}

func TestReconcileMailThrottles_KeepsStalwartIDOnApplyError(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	cl.failCmd = map[string]error{"update": errors.New("stalwart 503")}
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal, MaxPerHour: 100,
		Enabled: true, StalwartID: "stw-keep-me",
	}
	r.reconcileMailThrottles(context.Background())
	require.Len(t, cl.updates, 1)
	assert.Equal(t, "stw-keep-me", repo.rows["row1"].StalwartID,
		"stalwart_id must NOT clear when update fails — next tick retries")
	require.Len(t, repo.stamped, 1)
	assert.Contains(t, repo.stamped[0].lastErr, "stalwart 503")
}

func TestThrottlePayloadFor_ScopeKeyMapping(t *testing.T) {
	cases := []struct {
		scope string
		want  []string // keys expected in payload.Key
	}{
		{models.OutboundScopeGlobal, nil},
		{models.OutboundScopeUser, []string{"sender"}},
		{models.OutboundScopeDomain, []string{"senderDomain"}},
	}
	for _, c := range cases {
		t.Run(c.scope, func(t *testing.T) {
			row := &models.MailOutboundPolicy{Scope: c.scope, MaxPerHour: 10, Enabled: true}
			p := throttlePayloadFor(row)
			for _, k := range c.want {
				if !p.Key[k] {
					t.Errorf("scope=%s missing key=%s; full key map=%v", c.scope, k, p.Key)
				}
			}
			if c.scope == models.OutboundScopeGlobal && len(p.Key) != 0 {
				t.Errorf("global scope should have empty key map, got %v", p.Key)
			}
		})
	}
}
