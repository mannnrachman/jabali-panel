package cronops

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/cronvalidate"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// --- fakes ---

type fakeUsers struct{ u *models.User }

func (f fakeUsers) FindByID(_ context.Context, id string) (*models.User, error) {
	if f.u == nil || f.u.ID != id {
		return nil, repository.ErrNotFound
	}
	return f.u, nil
}

type fakeDomains struct{ docroots []string }

func (f fakeDomains) ListByUserID(_ context.Context, _ string, _ repository.ListOptions) ([]models.Domain, int64, error) {
	ds := make([]models.Domain, 0, len(f.docroots))
	for _, d := range f.docroots {
		ds = append(ds, models.Domain{DocRoot: d})
	}
	return ds, int64(len(ds)), nil
}

type fakeCronRepo struct {
	created *models.CronJob
	deleted string
}

func (f *fakeCronRepo) Create(_ context.Context, j *models.CronJob) error { f.created = j; return nil }
func (f *fakeCronRepo) Delete(_ context.Context, id string) error         { f.deleted = id; return nil }
func (f *fakeCronRepo) FindByID(_ context.Context, id string) (*models.CronJob, error) {
	if f.created != nil && f.created.ID == id {
		return f.created, nil
	}
	return nil, repository.ErrNotFound
}
func (f *fakeCronRepo) Update(_ context.Context, j *models.CronJob) error { f.created = j; return nil }

type fakeAgent struct {
	called bool
	method string
	fail   bool
}

func (f *fakeAgent) Call(_ context.Context, m string, _ any) (json.RawMessage, error) {
	f.called, f.method = true, m
	if f.fail {
		return nil, errors.New("agent boom")
	}
	return json.RawMessage(`{}`), nil
}

func uname(s string) *string { return &s }

func deps(u *models.User, agent *fakeAgent, cr *fakeCronRepo) Deps {
	return Deps{
		Users:    fakeUsers{u: u},
		Domains:  fakeDomains{docroots: []string{"/var/www/site"}},
		CronJobs: cr,
		Agent:    agent,
	}
}

// Drift reproducer (ADR-0101): Cron Job Intake must agent-apply
// synchronously for an enabled job AND roll the row back if apply
// fails — the contract REST had and the CLI silently lacked.
func TestCreate_EnabledAppliesAndRollsBackOnAgentFail(t *testing.T) {
	u := &models.User{ID: "u1", Username: uname("alice")}

	// happy path: enabled → agent cron.apply called, row persisted
	ag := &fakeAgent{}
	cr := &fakeCronRepo{}
	job, err := Create(context.Background(), deps(u, ag, cr), CreateInput{
		UserID: "u1", Name: "nightly", Schedule: "*/5 * * * *",
		Command: "/usr/bin/php -v", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !ag.called || ag.method != "cron.apply" {
		t.Fatalf("enabled create must agent-apply cron.apply, got called=%v method=%q", ag.called, ag.method)
	}
	if cr.created == nil || job == nil || cr.created.ID != job.ID {
		t.Fatal("job not persisted")
	}

	// agent failure → row rolled back, ErrAgentFailed
	ag2 := &fakeAgent{fail: true}
	cr2 := &fakeCronRepo{}
	_, err = Create(context.Background(), deps(u, ag2, cr2), CreateInput{
		UserID: "u1", Name: "nightly", Schedule: "*/5 * * * *",
		Command: "/usr/bin/php -v", Enabled: true,
	})
	if !errors.Is(err, ErrAgentFailed) {
		t.Fatalf("agent fail must return ErrAgentFailed, got %v", err)
	}
	if cr2.deleted != cr2.created.ID || cr2.created == nil {
		t.Fatalf("row must be rolled back on agent failure (deleted=%q)", cr2.deleted)
	}
}

func TestCreate_ValidationAndLinuxGate(t *testing.T) {
	withName := &models.User{ID: "u1", Username: uname("alice")}
	noLinux := &models.User{ID: "u1"} // Username nil

	for _, tc := range []struct {
		name string
		u    *models.User
		in   CreateInput
		want error
	}{
		{"bad name", withName, CreateInput{UserID: "u1", Name: "bad\x00", Schedule: "*/5 * * * *", Command: "/usr/bin/php -v"}, ErrNameInvalid},
		{"no linux account", noLinux, CreateInput{UserID: "u1", Name: "ok", Schedule: "*/5 * * * *", Command: "/usr/bin/php -v"}, cronvalidate.ErrNoLinuxAccount},
		{"bad schedule", withName, CreateInput{UserID: "u1", Name: "ok", Schedule: "nope", Command: "/usr/bin/php -v"}, ErrScheduleInvalid},
		{"bad command", withName, CreateInput{UserID: "u1", Name: "ok", Schedule: "*/5 * * * *", Command: "rm -rf /"}, ErrCommandInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := &fakeAgent{}
			_, err := Create(context.Background(), deps(tc.u, ag, &fakeCronRepo{}), tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want errors.Is %v, got %v", tc.want, err)
			}
			if ag.called {
				t.Fatal("must not agent-apply when validation fails")
			}
		})
	}
}

func TestCreate_DisabledDoesNotApply(t *testing.T) {
	u := &models.User{ID: "u1", Username: uname("alice")}
	ag := &fakeAgent{}
	_, err := Create(context.Background(), deps(u, ag, &fakeCronRepo{}), CreateInput{
		UserID: "u1", Name: "nightly", Schedule: "*/5 * * * *",
		Command: "/usr/bin/php -v", Enabled: false,
	})
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	if ag.called {
		t.Fatal("disabled job must NOT agent-apply at intake")
	}
}

// Wire-contract guard for the canonical cron.apply / cron.remove
// structs (relocated here from api per ADR-0101). panel-agent
// cron.* parse these exact JSON keys — drift = silent runtime
// validation failure (feedback_cross_boundary_contracts). Change
// this AND panel-agent/internal/commands/cron_*.go together.
func TestCronWireShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload any
		want    []string
	}{
		{"cron.apply", applyParams{UserID: "u", Username: "s", JobID: "j", Name: "n", Command: "wp cron", Schedule: "0 * * * *", OwnedDocroots: []string{"/x"}},
			[]string{"command", "job_id", "name", "owned_docroots", "schedule", "user_id", "username"}},
		{"cron.remove", removeParams{UserID: "u", Username: "s", JobID: "j"},
			[]string{"job_id", "user_id", "username"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.payload)
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := make([]string, 0, len(m))
			for k := range m {
				got = append(got, k)
			}
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("key count: got %v want %v", got, tc.want)
			}
			for i, k := range got {
				if k != tc.want[i] {
					t.Fatalf("key[%d]=%q want %q", i, k, tc.want[i])
				}
			}
		})
	}
}
