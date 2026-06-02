package reconciler

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// fakeRuntimeAgent mock
type fakeRuntimeAgent struct {
	mu           sync.Mutex
	calls        []fakeCall
	statusFailed bool
}

func (f *fakeRuntimeAgent) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{method, params})
	f.mu.Unlock()

	switch method {
	case "domain.list":
		return json.Marshal(map[string][]string{
			"sites": {},
		})
	case "domain.create":
		return json.Marshal(map[string]string{"status": "created"})
	case "runtime.deploy":
		return json.Marshal(map[string]string{"output": "npm install successful\nbuilt successfully"})
	case "runtime.apply":
		return json.Marshal(map[string]string{"service_path": "/home/alice/.config/systemd/user/jabali-rt-testdomain.com.service"})
	case "runtime.status":
		if f.statusFailed {
			return json.Marshal(map[string]any{
				"status":    "failed",
				"sub_state": "crashed",
				"pid":       0,
			})
		}
		return json.Marshal(map[string]any{
			"status":    "active",
			"sub_state": "running",
			"pid":       12345,
		})
	case "runtime.logs":
		return json.Marshal(map[string]string{"logs": "Error: Connection refused\nProcess crashed."})
	default:
		return nil, nil
	}
}

// fakeRuntimeServiceRepo mock
type fakeRuntimeServiceRepo struct {
	mu       sync.Mutex
	services map[string]*models.RuntimeService
}

func (f *fakeRuntimeServiceRepo) Create(ctx context.Context, s *models.RuntimeService) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services[s.DomainID] = s
	return nil
}

func (f *fakeRuntimeServiceRepo) FindByID(ctx context.Context, id string) (*models.RuntimeService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.services {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeRuntimeServiceRepo) FindByDomainID(ctx context.Context, domainID string) (*models.RuntimeService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.services[domainID]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return s, nil
}

func (f *fakeRuntimeServiceRepo) FindByUserID(ctx context.Context, userID string) ([]models.RuntimeService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var res []models.RuntimeService
	for _, s := range f.services {
		if s.UserID == userID {
			res = append(res, *s)
		}
	}
	return res, nil
}

func (f *fakeRuntimeServiceRepo) ListAll(ctx context.Context, opts repository.ListOptions) ([]models.RuntimeService, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var res []models.RuntimeService
	for _, s := range f.services {
		res = append(res, *s)
	}
	return res, int64(len(res)), nil
}

func (f *fakeRuntimeServiceRepo) ListByStatus(ctx context.Context, status string) ([]models.RuntimeService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var res []models.RuntimeService
	for _, s := range f.services {
		if s.Status == status {
			res = append(res, *s)
		}
	}
	return res, nil
}

func (f *fakeRuntimeServiceRepo) Update(ctx context.Context, s *models.RuntimeService) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.services[s.DomainID] = s
	return nil
}

func (f *fakeRuntimeServiceRepo) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for domainID, s := range f.services {
		if s.ID == id {
			delete(f.services, domainID)
			return nil
		}
	}
	return nil
}

func (f *fakeRuntimeServiceRepo) SetStatus(ctx context.Context, id, status string, lastErr *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.services {
		if s.ID == id {
			s.Status = status
			s.LastError = lastErr
			return nil
		}
	}
	return repository.ErrNotFound
}

func (f *fakeRuntimeServiceRepo) IsPortInUse(ctx context.Context, port uint32) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.services {
		if s.ListenPort == port {
			return true, nil
		}
	}
	return false, nil
}

func TestReconcile_RuntimeService_AutoProvisionAndDeploy(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeRuntimeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	rtRepo := &fakeRuntimeServiceRepo{services: make(map[string]*models.RuntimeService)}

	username := "alice"
	user := &models.User{
		ID:       "user-alice",
		Email:    "alice@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain := &models.Domain{
		ID:          "domain-1",
		UserID:      user.ID,
		Name:        "testdomain.com",
		DocRoot:     "/home/alice/domains/testdomain.com/public_html",
		RuntimeType: "nodejs",
		IsEnabled:   true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithRuntimeServices(rtRepo)

	// Step A: Run ReconcileAll once. It provisions the service row
	// synchronously, then dispatches deploy+apply to a background
	// goroutine (so a slow npm install / docker build can't block the
	// reconcile loop). Poll until the goroutine converges the status.
	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	var svc *models.RuntimeService
	require.Eventually(t, func() bool {
		s, e := rtRepo.FindByDomainID(ctx, domain.ID)
		if e != nil || s == nil {
			return false
		}
		svc = s
		return s.Status == "running"
	}, 5*time.Second, 20*time.Millisecond, "runtime service should converge to running")

	// Verify that RuntimeService was auto-provisioned with expected shape
	require.NotNil(t, svc)
	require.Equal(t, "nodejs", svc.Runtime)
	require.Equal(t, "running", svc.Status)
	require.True(t, svc.ListenPort >= 10000 && svc.ListenPort <= 60000)
	require.Equal(t, "jabali-rt-testdomain.com.service", svc.SystemdUnit)

	// Verify agent was called with correct sequence of methods
	agent.mu.Lock()
	var methods []string
	for _, call := range agent.calls {
		methods = append(methods, call.method)
	}
	agent.mu.Unlock()

	require.Contains(t, methods, "runtime.deploy")
	require.Contains(t, methods, "runtime.apply")
	require.Contains(t, methods, "domain.create")

	// Ensure the proxy port params were supplied to domain.create
	var createParams map[string]any
	agent.mu.Lock()
	for _, call := range agent.calls {
		if call.method == "domain.create" {
			createParams = call.params.(map[string]any)
		}
	}
	agent.mu.Unlock()

	require.NotNil(t, createParams)
	require.Equal(t, "nodejs", createParams["runtime_type"])
	require.Equal(t, int(svc.ListenPort), createParams["proxy_port"])
}

func TestReconcile_RuntimeService_TransitionsToPHP(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeRuntimeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	rtRepo := &fakeRuntimeServiceRepo{services: make(map[string]*models.RuntimeService)}

	username := "alice"
	user := &models.User{
		ID:       "user-alice",
		Email:    "alice@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain := &models.Domain{
		ID:          "domain-1",
		UserID:      user.ID,
		Name:        "testdomain.com",
		DocRoot:     "/home/alice/domains/testdomain.com/public_html",
		RuntimeType: "php", // Changed to PHP
		IsEnabled:   true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	domainRepo.domains[domain.ID] = domain

	// Pre-populate with an existing running service
	existingSvc := &models.RuntimeService{
		ID:          "svc-1",
		DomainID:    domain.ID,
		UserID:      user.ID,
		Runtime:     "nodejs",
		ListenPort:  12345,
		Status:      "running",
		SystemdUnit: "jabali-rt-testdomain.com.service",
	}
	rtRepo.services[domain.ID] = existingSvc

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithRuntimeServices(rtRepo)

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// The runtime service should be stopped and deleted!
	svc, err := rtRepo.FindByDomainID(ctx, domain.ID)
	require.Error(t, err)
	require.Nil(t, svc)

	agent.mu.Lock()
	var methods []string
	for _, call := range agent.calls {
		methods = append(methods, call.method)
	}
	agent.mu.Unlock()

	require.Contains(t, methods, "runtime.remove")
}

func TestReconcile_RuntimeService_MonitoringAndLogs(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// statusFailed: true makes the agent report a crashed state
	agent := &fakeRuntimeAgent{statusFailed: true}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	rtRepo := &fakeRuntimeServiceRepo{services: make(map[string]*models.RuntimeService)}

	username := "alice"
	user := &models.User{
		ID:       "user-alice",
		Email:    "alice@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain := &models.Domain{
		ID:          "domain-1",
		UserID:      user.ID,
		Name:        "testdomain.com",
		DocRoot:     "/home/alice/domains/testdomain.com/public_html",
		RuntimeType: "python",
		IsEnabled:   true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	domainRepo.domains[domain.ID] = domain

	// Pre-populate with an existing running service
	existingSvc := &models.RuntimeService{
		ID:          "svc-1",
		DomainID:    domain.ID,
		UserID:      user.ID,
		Runtime:     "python",
		ListenPort:  12345,
		Status:      "running",
		SystemdUnit: "jabali-rt-testdomain.com.service",
	}
	rtRepo.services[domain.ID] = existingSvc

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithRuntimeServices(rtRepo)

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Since status is failed, the service status should transition to failed and capture logs!
	svc, err := rtRepo.FindByDomainID(ctx, domain.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", svc.Status)
	require.NotNil(t, svc.LastError)
	require.Contains(t, *svc.LastError, "Process crashed")

	agent.mu.Lock()
	var methods []string
	for _, call := range agent.calls {
		methods = append(methods, call.method)
	}
	agent.mu.Unlock()

	require.Contains(t, methods, "runtime.status")
	require.Contains(t, methods, "runtime.logs")
}
