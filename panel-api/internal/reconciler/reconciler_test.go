package reconciler

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// fakeAgent mocks the agent.AgentInterface for testing.
type fakeAgent struct {
	calls []fakeCall
}

type fakeCall struct {
	method string
	params interface{}
}

func (f *fakeAgent) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{method, params})

	switch method {
	case "domain.list":
		return json.Marshal(map[string][]string{
			"sites": {"example.com", "foo.bar.com"},
		})
	case "domain.create":
		return json.Marshal(map[string]string{"domain": "", "status": "created"})
	case "domain.disable":
		return json.Marshal(map[string]string{"domain": "", "enabled": "false"})
	default:
		return nil, nil
	}
}

// fakeDomainRepo mocks the domain repository.
type fakeDomainRepo struct {
	domains map[string]*models.Domain
}

func (f *fakeDomainRepo) Create(ctx context.Context, d *models.Domain) error {
	f.domains[d.ID] = d
	return nil
}

func (f *fakeDomainRepo) FindByID(ctx context.Context, id string) (*models.Domain, error) {
	d, ok := f.domains[id]
	if !ok {
		return nil, &notFoundErr{}
	}
	return d, nil
}

func (f *fakeDomainRepo) FindByName(ctx context.Context, name string) (*models.Domain, error) {
	for _, d := range f.domains {
		if d.Name == name {
			return d, nil
		}
	}
	return nil, &notFoundErr{}
}

func (f *fakeDomainRepo) List(ctx context.Context, offset, limit int) ([]models.Domain, int64, error) {
	var result []models.Domain
	for _, d := range f.domains {
		result = append(result, *d)
	}
	return result, int64(len(result)), nil
}

func (f *fakeDomainRepo) ListByUserID(ctx context.Context, userID string, offset, limit int) ([]models.Domain, int64, error) {
	var result []models.Domain
	for _, d := range f.domains {
		if d.UserID == userID {
			result = append(result, *d)
		}
	}
	return result, int64(len(result)), nil
}

func (f *fakeDomainRepo) Update(ctx context.Context, d *models.Domain) error {
	f.domains[d.ID] = d
	return nil
}

func (f *fakeDomainRepo) Delete(ctx context.Context, id string) error {
	delete(f.domains, id)
	return nil
}

func (f *fakeDomainRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	count := 0
	for _, d := range f.domains {
		if d.UserID == userID {
			count++
		}
	}
	return int64(count), nil
}

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "not found" }
func (e *notFoundErr) Is(err error) bool {
	_, ok := err.(*notFoundErr)
	return ok
}

// fakeUserRepo mocks the user repository.
type fakeUserRepo struct {
	users map[string]*models.User
}

func (f *fakeUserRepo) Create(ctx context.Context, u *models.User) error {
	f.users[u.ID] = u
	return nil
}

func (f *fakeUserRepo) FindByID(ctx context.Context, id string) (*models.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, &notFoundErr{}
	}
	return u, nil
}

func (f *fakeUserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, &notFoundErr{}
}

func (f *fakeUserRepo) FindByUsername(ctx context.Context, username string) (*models.User, error) {
	for _, u := range f.users {
		if u.Username != nil && *u.Username == username {
			return u, nil
		}
	}
	return nil, &notFoundErr{}
}

func (f *fakeUserRepo) List(ctx context.Context, offset, limit int) ([]models.User, int64, error) {
	var result []models.User
	for _, u := range f.users {
		result = append(result, *u)
	}
	return result, int64(len(result)), nil
}

func (f *fakeUserRepo) Update(ctx context.Context, u *models.User) error {
	f.users[u.ID] = u
	return nil
}

func (f *fakeUserRepo) Delete(ctx context.Context, id string) error {
	delete(f.users, id)
	return nil
}

func (f *fakeUserRepo) SetAdmin(ctx context.Context, id string, isAdmin bool) error {
	if u, ok := f.users[id]; ok {
		u.IsAdmin = isAdmin
	}
	return nil
}

func (f *fakeUserRepo) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	for _, u := range f.users {
		if u.IsAdmin {
			n++
		}
	}
	return n, nil
}

func TestReconcileAll_EnabledDomainMissing(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// Setup: one enabled domain in DB, but missing from agent
	now := time.Now().UTC()
	username := "alice"
	user := &models.User{
		ID:       "user-1",
		Email:    "alice@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain := &models.Domain{
		ID:        "domain-1",
		UserID:    user.ID,
		Name:      "missing.com",
		DocRoot:   "/home/alice/domains/missing.com/public_html",
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Verify that domain.create was called
	require.Len(t, agent.calls, 2) // domain.list + domain.create
	require.Equal(t, "domain.list", agent.calls[0].method)
	require.Equal(t, "domain.create", agent.calls[1].method)
}

func TestReconcileAll_DisabledDomainPresent(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// Setup: one disabled domain in DB, but present on agent
	now := time.Now().UTC()
	username := "bob"
	user := &models.User{
		ID:       "user-1",
		Email:    "bob@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain := &models.Domain{
		ID:        "domain-2",
		UserID:    user.ID,
		Name:      "example.com",
		DocRoot:   "/home/bob/domains/example.com/public_html",
		IsEnabled: false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Verify that domain.disable was called
	require.Len(t, agent.calls, 2) // domain.list + domain.disable
	require.Equal(t, "domain.list", agent.calls[0].method)
	require.Equal(t, "domain.disable", agent.calls[1].method)
}

func TestReconcileAll_OrphanLogsWarning(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// agent returns "orphan.com" which doesn't exist in DB
	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Verify that domain.list was called but no creates/disables
	require.Len(t, agent.calls, 1)
	require.Equal(t, "domain.list", agent.calls[0].method)
}

func TestReconcileOne_DomainFound(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	now := time.Now().UTC()
	username := "charlie"
	user := &models.User{
		ID:       "user-1",
		Email:    "charlie@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain := &models.Domain{
		ID:        "domain-3",
		UserID:    user.ID,
		Name:      "test.com",
		DocRoot:   "/home/charlie/domains/test.com/public_html",
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	err := r.ReconcileOne(ctx, domain.ID)
	require.NoError(t, err)

	// Verify that domain.create was called
	require.Len(t, agent.calls, 1)
	require.Equal(t, "domain.create", agent.calls[0].method)
}

func TestReconcileOne_DomainNotFound(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	// Non-existent domain ID
	err := r.ReconcileOne(ctx, "nonexistent")
	require.NoError(t, err)

	// Should not call agent since we don't know the domain name
	require.Len(t, agent.calls, 0)
}

func TestSchedule_NonBlocking(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second, QueueLen: 2})

	// Schedule should not block
	r.Schedule("domain-1")
	r.Schedule("domain-2")
	r.Schedule("domain-3") // Should drop silently
}

func TestLinuxUserFromEmail(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"alice@example.com", "alice"},
		{"bob.smith@company.org", "bob.smith"},
		{"user+tag@domain.io", "user+tag"},
		{"simple", "simple"}, // no @ sign
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := linuxUserFromEmail(tt.email)
			require.Equal(t, tt.expected, result)
		})
	}
}
