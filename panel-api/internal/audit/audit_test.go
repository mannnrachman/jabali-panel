package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// --- fakes (narrow, per the convention) ---

type fakeCreator struct {
	mu   sync.Mutex
	got  []*models.AuditEvent
	done chan *models.AuditEvent
	err  error
}

func newFakeCreator() *fakeCreator { return &fakeCreator{done: make(chan *models.AuditEvent, 4)} }

func (f *fakeCreator) Create(_ context.Context, e *models.AuditEvent) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	f.got = append(f.got, e)
	f.mu.Unlock()
	f.done <- e
	return nil
}

type errPublisher struct{ called bool }

func (p *errPublisher) Publish(context.Context, *models.AuditEvent) (string, error) {
	p.called = true
	return "", context.DeadlineExceeded
}

func waitEvent(t *testing.T, ch <-chan *models.AuditEvent) *models.AuditEvent {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for audit event")
		return nil
	}
}

// --- canonical / hash chain (pure, deterministic) ---

func TestCanonical_DeterministicAndSensitive(t *testing.T) {
	e := &models.AuditEvent{
		ID: "01HZ", TS: time.Unix(0, 1234).UTC(), ActorKind: models.AuditActorAdmin,
		Action: "POST /x", TargetType: "user", TargetID: "u1", Result: models.AuditResultOK,
	}
	require.Equal(t, canonical(e), canonical(e), "canonical must be deterministic")

	e2 := *e
	e2.Result = models.AuditResultDenied
	require.NotEqual(t, canonical(e), canonical(&e2), "a field change must change canonical")
}

func TestComputeRowHash_ChainsOnPrevAndNoBleed(t *testing.T) {
	e := &models.AuditEvent{ID: "01HZ", TS: time.Unix(0, 1).UTC(), Action: "a", Result: "ok"}

	h1 := computeRowHash("", e)
	h2 := computeRowHash("prevX", e)
	require.NotEqual(t, h1, h2, "different prev must yield different row hash")
	require.Equal(t, h2, computeRowHash("prevX", e), "deterministic for same (prev,event)")
	require.Len(t, h1, 64, "sha256 hex")

	// Record-separator: "a"+"" must not collide with ""+"a" style bleed.
	ea := &models.AuditEvent{ID: "a", TS: time.Unix(0, 1).UTC(), Result: "ok"}
	eb := &models.AuditEvent{ID: "", TS: time.Unix(0, 1).UTC(), Result: "ok"}
	require.NotEqual(t, computeRowHash("Z", ea), computeRowHash("Za", eb))
}

func TestStreamMap_RoundTrip(t *testing.T) {
	sub := "user42"
	e := &models.AuditEvent{
		ID: "01HZ", TS: time.Unix(0, 99).UTC(), ActorKind: models.AuditActorUser,
		SubjectUserID: &sub, Action: "POST /api/v1/files/upload", TargetType: "file",
		TargetID: "/x", Result: models.AuditResultOK, Meta: []byte(`{"k":1}`),
	}
	round := parseStream(streamMap(e))
	require.Equal(t, e.ID, round.ID)
	require.Equal(t, e.TS.UnixNano(), round.TS.UnixNano())
	require.Equal(t, e.ActorKind, round.ActorKind)
	require.NotNil(t, round.SubjectUserID)
	require.Equal(t, sub, *round.SubjectUserID)
	require.Equal(t, e.Action, round.Action)
	require.Nil(t, round.ActorUserID, "empty nullable must round-trip to nil, not \"\"")
	require.JSONEq(t, `{"k":1}`, string(round.Meta))
}

// --- recorder fallback (never blocks; persists when stream unavailable) ---

func TestRecorder_FallbackWhenNoQueue(t *testing.T) {
	fc := newFakeCreator()
	r := NewRecorder(nil, fc, nil) // nil queue => straight to DB fallback

	r.Record(&models.AuditEvent{Action: "x"}) // no ID/TS/result set

	got := waitEvent(t, fc.done)
	require.NotEmpty(t, got.ID, "recorder must mint a ULID")
	require.False(t, got.TS.IsZero(), "recorder must stamp TS")
	require.Equal(t, models.AuditResultOK, got.Result, "default result")
	require.Equal(t, models.AuditActorSystem, got.ActorKind, "default actor kind")
}

func TestRecorder_FallbackWhenPublishFails(t *testing.T) {
	fc := newFakeCreator()
	pub := &errPublisher{}
	r := NewRecorder(pub, fc, nil)

	r.Record(ImpersonationStart("admin1", "user9", "1.2.3.4", "req1"))

	got := waitEvent(t, fc.done)
	require.True(t, pub.called, "publish must be attempted first")
	require.Equal(t, "impersonation.start", got.Action)
	require.NotNil(t, got.SubjectUserID)
	require.Equal(t, "user9", *got.SubjectUserID, "impersonated user is the subject")
	require.Equal(t, models.AuditActorAdmin, got.ActorKind)
}

func TestConstructors_SubjectScoping(t *testing.T) {
	// Server-scoped events MUST have a nil subject (admin-only by
	// construction — invisible to /me/activity).
	st := SecurityToggle("admin1", "crowdsec", "off", "", "")
	require.Nil(t, st.SubjectUserID)

	// API mutation with empty subject → nil (safe-fail).
	m := APIMutation("a", models.AuditActorUser, "", "GET /x", "t", "id", "ok", "", "")
	require.Nil(t, m.SubjectUserID)

	// Break-glass: actor is its own subject (shows in admin's feed).
	bg := BreakGlassLogin("admin1", "cli_login", "9.9.9.9")
	require.NotNil(t, bg.SubjectUserID)
	require.Equal(t, "admin1", *bg.SubjectUserID)
	require.Equal(t, models.AuditActorCLI, bg.ActorKind)
}
