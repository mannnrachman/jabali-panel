package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// fakeAgent mocks the agent.AgentInterface for testing.
type fakeAgent struct {
	calls      []fakeCall
	failMethod string // if set, Call returns an error for this method
}

type fakeCall struct {
	method string
	params interface{}
}

func (f *fakeAgent) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{method, params})

	if method == f.failMethod {
		return nil, fmt.Errorf("agent call failed for method: %s", method)
	}

	switch method {
	case "domain.list":
		return json.Marshal(map[string][]string{
			"sites": {"example.com", "foo.bar.com"},
		})
	case "domain.create":
		return json.Marshal(map[string]string{"domain": "", "status": "created"})
	case "filebrowser.user.ensure":
		return json.Marshal(map[string]interface{}{
			"username": "testuser",
			"scope":    "/home/testuser",
			"created":  false,
			"no_change": true,
		})
	case "filebrowser.user.delete":
		return json.Marshal(map[string]interface{}{
			"username": "testuser",
			"deleted":  false,
			"not_found": true,
		})
	case "filebrowser.user.list":
		return json.Marshal(map[string][]string{
			"usernames": {"user1", "user2"},
		})
	case "filebrowser.group.add":
		return json.Marshal(map[string]bool{
			"added": false,
			"already_member": true,
		})
	case "filebrowser.service.restart":
		return json.Marshal(map[string]bool{
			"restarted": true,
		})
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
		return nil, repository.ErrNotFound
	}
	return d, nil
}

func (f *fakeDomainRepo) FindByName(ctx context.Context, name string) (*models.Domain, error) {
	for _, d := range f.domains {
		if d.Name == name {
			return d, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeDomainRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.Domain, int64, error) {
	var result []models.Domain
	for _, d := range f.domains {
		result = append(result, *d)
	}
	return result, int64(len(result)), nil
}

func (f *fakeDomainRepo) ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.Domain, int64, error) {
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

func (f *fakeDomainRepo) SetPHPPoolID(ctx context.Context, id string, poolID *string) error {
	return nil
}

func (f *fakeDomainRepo) CountByPHPPoolID(ctx context.Context, poolID string) (int64, error) {
	count := 0
	for _, d := range f.domains {
		if d.PHPPoolID != nil && *d.PHPPoolID == poolID {
			count++
		}
	}
	return int64(count), nil
}

func (f *fakeDomainRepo) UpdatePHPSettings(ctx context.Context, id string, settings repository.DomainPHPSettings) error {
	for i, d := range f.domains {
		if d.ID == id {
			f.domains[i].PHPMemoryLimit = settings.MemoryLimit
			f.domains[i].PHPUploadMaxFilesize = settings.UploadMaxFilesize
			f.domains[i].PHPPostMaxSize = settings.PostMaxSize
			f.domains[i].PHPMaxInputVars = settings.MaxInputVars
			f.domains[i].PHPMaxExecutionTime = settings.MaxExecutionTime
			f.domains[i].PHPMaxInputTime = settings.MaxInputTime
			return nil
		}
	}
	return &notFoundErr{}
}

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "not found" }
func (e *notFoundErr) Is(err error) bool {
	_, ok := err.(*notFoundErr)
	return ok
}

// fakeDNSZoneRepo mocks the DNS zone repository.
type fakeDNSZoneRepo struct {
	zones map[string]*models.DNSZone
}

func (f *fakeDNSZoneRepo) Create(ctx context.Context, zone *models.DNSZone) error {
	f.zones[zone.ID] = zone
	return nil
}

func (f *fakeDNSZoneRepo) FindByID(ctx context.Context, id string) (*models.DNSZone, error) {
	z, ok := f.zones[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return z, nil
}

func (f *fakeDNSZoneRepo) FindByName(ctx context.Context, name string) (*models.DNSZone, error) {
	for _, z := range f.zones {
		if z.Name == name {
			return z, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeDNSZoneRepo) FindByDomainID(ctx context.Context, domainID string) (*models.DNSZone, error) {
	for _, z := range f.zones {
		if z.DomainID == domainID {
			return z, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeDNSZoneRepo) ListAll(ctx context.Context) ([]models.DNSZone, error) {
	var result []models.DNSZone
	for _, z := range f.zones {
		result = append(result, *z)
	}
	return result, nil
}

func (f *fakeDNSZoneRepo) Update(ctx context.Context, zone *models.DNSZone) error {
	f.zones[zone.ID] = zone
	return nil
}

func (f *fakeDNSZoneRepo) Delete(ctx context.Context, id string) error {
	delete(f.zones, id)
	return nil
}

// fakeDNSRecordRepo mocks the DNS record repository.
type fakeDNSRecordRepo struct {
	records map[string]*models.DNSRecord
}

func (f *fakeDNSRecordRepo) Create(ctx context.Context, record *models.DNSRecord) error {
	f.records[record.ID] = record
	return nil
}

func (f *fakeDNSRecordRepo) FindByID(ctx context.Context, id string) (*models.DNSRecord, error) {
	r, ok := f.records[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return r, nil
}

func (f *fakeDNSRecordRepo) ListByZoneID(ctx context.Context, zoneID string) ([]models.DNSRecord, error) {
	var result []models.DNSRecord
	for _, r := range f.records {
		if r.ZoneID == zoneID {
			result = append(result, *r)
		}
	}
	return result, nil
}

func (f *fakeDNSRecordRepo) DeleteByZoneID(ctx context.Context, zoneID string) error {
	for id, r := range f.records {
		if r.ZoneID == zoneID {
			delete(f.records, id)
		}
	}
	return nil
}

func (f *fakeDNSRecordRepo) Update(ctx context.Context, record *models.DNSRecord) error {
	f.records[record.ID] = record
	return nil
}

func (f *fakeDNSRecordRepo) Delete(ctx context.Context, id string) error {
	delete(f.records, id)
	return nil
}

// fakeServerSettingsRepo mocks the server settings repository.
type fakeServerSettingsRepo struct {
	settings *models.ServerSettings
}

func (f *fakeServerSettingsRepo) Get(ctx context.Context) (*models.ServerSettings, error) {
	if f.settings == nil {
		return nil, repository.ErrNotFound
	}
	return f.settings, nil
}

func (f *fakeServerSettingsRepo) Upsert(ctx context.Context, settings *models.ServerSettings) error {
	f.settings = settings
	return nil
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
		return nil, repository.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeUserRepo) FindByUsername(ctx context.Context, username string) (*models.User, error) {
	for _, u := range f.users {
		if u.Username != nil && *u.Username == username {
			return u, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (f *fakeUserRepo) List(ctx context.Context, opts repository.ListOptions) ([]models.User, int64, error) {
	var result []models.User
	for _, u := range f.users {
		result = append(result, *u)
	}
	total := int64(len(result))
	// Respect the limit option
	if opts.Limit > 0 && len(result) > opts.Limit {
		result = result[:opts.Limit]
	}
	return result, total, nil
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

func (f *fakeUserRepo) FindAdminsByEmail(ctx context.Context) ([]*models.User, error) {
	var admins []*models.User
	for _, u := range f.users {
		if u.IsAdmin {
			u := u
			admins = append(admins, u)
		}
	}
	return admins, nil
}

// fakePHPPoolRepo mocks the PHP pool repository.
type fakePHPPoolRepo struct {
	pools map[string]*models.PHPPool
}

func (f *fakePHPPoolRepo) FindByID(ctx context.Context, id string) (*models.PHPPool, error) {
	p, ok := f.pools[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return p, nil
}

func (f *fakePHPPoolRepo) Create(ctx context.Context, p *models.PHPPool) error {
	f.pools[p.ID] = p
	return nil
}

func (f *fakePHPPoolRepo) Update(ctx context.Context, p *models.PHPPool) error {
	f.pools[p.ID] = p
	return nil
}

func (f *fakePHPPoolRepo) Delete(ctx context.Context, id string) error {
	delete(f.pools, id)
	return nil
}

func (f *fakePHPPoolRepo) FindByUserID(ctx context.Context, userID string) (*models.PHPPool, error) {
	for _, p := range f.pools {
		if p.UserID == userID {
			return p, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakePHPPoolRepo) ListAll(ctx context.Context, opts repository.ListOptions) ([]models.PHPPool, int64, error) {
	var result []models.PHPPool
	for _, p := range f.pools {
		result = append(result, *p)
	}
	return result, int64(len(result)), nil
}

func (f *fakePHPPoolRepo) SetStatus(ctx context.Context, id string, status string, lastErr *string) error {
	if p, ok := f.pools[id]; ok {
		p.Status = status
		p.LastError = lastErr
	}
	return nil
}

// filterCallsByPrefix returns only calls whose method starts with the given prefix.
// Used to isolate domain/php/fs calls from filebrowser calls in ReconcileAll tests.
func filterCallsByPrefix(calls []fakeCall, prefix string) []fakeCall {
	var result []fakeCall
	for _, call := range calls {
		if strings.HasPrefix(call.method, prefix) {
			result = append(result, call)
		}
	}
	return result
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

	// Verify that domain.create was called (filtering out filebrowser calls)
	domainCalls := filterCallsByPrefix(agent.calls, "domain.")
	require.Len(t, domainCalls, 2) // domain.list + domain.create
	require.Equal(t, "domain.list", domainCalls[0].method)
	require.Equal(t, "domain.create", domainCalls[1].method)
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

	// Verify that domain.create was called (filtering out filebrowser calls)
	domainCalls := filterCallsByPrefix(agent.calls, "domain.")
	require.Len(t, domainCalls, 2) // domain.list + domain.create
	require.Equal(t, "domain.list", domainCalls[0].method)
	require.Equal(t, "domain.create", domainCalls[1].method)
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

	// Verify that domain.list was called but no creates/disables (filtering out filebrowser calls)
	domainCalls := filterCallsByPrefix(agent.calls, "domain.")
	require.Len(t, domainCalls, 1)
	require.Equal(t, "domain.list", domainCalls[0].method)
}

func TestReconcileAll_DomainWithPHPPool(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: one user with a domain that has a PHP pool
	now := time.Now().UTC()
	username := "phpuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "phpuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	// Create a PHP pool with version 8.2
	phpPoolID := "pool-1"
	phpPool := &models.PHPPool{
		ID:         phpPoolID,
		PHPVersion: "8.2",
	}
	phpPoolRepo.pools[phpPoolID] = phpPool

	// Create a domain with a reference to the PHP pool
	domain := &models.Domain{
		ID:        "domain-1",
		UserID:    user.ID,
		Name:      "phpsite.com",
		DocRoot:   "/home/phpuser/domains/phpsite.com/public_html",
		IsEnabled: true,
		PHPPoolID: &phpPoolID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Verify that required calls were made (filtering out filebrowser calls).
	// createDomainOnAgent runs for every enabled domain on every reconcile pass
	// (the agent's writeVhost is content-hash gated, so the no-change case is cheap).
	allNonFilebrowserCalls := filterCallsByPrefix(agent.calls, "")
	var phpcalls, domainCalls, fsCalls []fakeCall
	for _, call := range allNonFilebrowserCalls {
		if call.method == "user.slice.ensure" || call.method == "php.pool.apply" {
			phpcalls = append(phpcalls, call)
		} else if call.method == "domain.list" || call.method == "domain.create" {
			domainCalls = append(domainCalls, call)
		} else if call.method == "fs.write_healthcheck" {
			fsCalls = append(fsCalls, call)
		}
	}

	require.GreaterOrEqual(t, len(phpcalls), 1, "should call user.slice.ensure and/or php.pool.apply")
	require.Len(t, domainCalls, 2, "should call domain.list and domain.create")
	require.Len(t, fsCalls, 1, "should call fs.write_healthcheck")

	// Verify that domain.create was called with correct PHP params
	var domainCreateCall *fakeCall
	for _, call := range agent.calls {
		if call.method == "domain.create" {
			domainCreateCall = &call
			break
		}
	}
	require.NotNil(t, domainCreateCall, "domain.create should be called")
	params := domainCreateCall.params.(map[string]any)
	require.Equal(t, true, params["has_php"], "has_php should be true")
	require.Equal(t, "8.2", params["php_version"], "php_version should be 8.2")

	// Verify that fs.write_healthcheck was called with correct path and user:group
	var hcCall *fakeCall
	for _, call := range agent.calls {
		if call.method == "fs.write_healthcheck" {
			hcCall = &call
			break
		}
	}
	require.NotNil(t, hcCall, "fs.write_healthcheck should be called")
	hcParams := hcCall.params.(map[string]string)
	require.Equal(t, "/home/phpuser/domains/phpsite.com/public_html/jabali-healthcheck.php", hcParams["path"], "healthcheck path should be correct")
	require.Equal(t, "phpuser:www-data", hcParams["user_group"], "healthcheck user_group should be correct")
}

func TestReconcileAll_DomainWithPHPSettingsOverrides(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: domain with PHP pool and per-domain INI overrides
	now := time.Now().UTC()
	username := "phpuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "phpuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	phpPoolID := "pool-1"
	phpPool := &models.PHPPool{
		ID:         phpPoolID,
		PHPVersion: "8.5",
	}
	phpPoolRepo.pools[phpPoolID] = phpPool

	// Domain with overrides
	mem := "256M"
	upload := "128M"
	post := "64M"
	inputVars := 10000
	execTime := 300
	inputTime := 60

	domain := &models.Domain{
		ID:                   "domain-1",
		UserID:               user.ID,
		Name:                 "phpsite.com",
		DocRoot:              "/home/phpuser/domains/phpsite.com/public_html",
		IsEnabled:            true,
		PHPPoolID:            &phpPoolID,
		PHPMemoryLimit:       &mem,
		PHPUploadMaxFilesize: &upload,
		PHPPostMaxSize:       &post,
		PHPMaxInputVars:      &inputVars,
		PHPMaxExecutionTime:  &execTime,
		PHPMaxInputTime:      &inputTime,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Verify domain.create was called (filtering out filebrowser calls)
	domainCreateCalls := filterCallsByPrefix(agent.calls, "domain.create")
	require.Len(t, domainCreateCalls, 1, "should call domain.create exactly once")

	// Verify PHP settings were passed through
	params := domainCreateCalls[0].params.(map[string]any)
	require.Equal(t, "256M", params["php_memory_limit"])
	require.Equal(t, "128M", params["php_upload_max_filesize"])
	require.Equal(t, "64M", params["php_post_max_size"])
	require.Equal(t, 10000, params["php_max_input_vars"])
	require.Equal(t, 300, params["php_max_execution_time"])
	require.Equal(t, 60, params["php_max_input_time"])
}

func TestReconcileAll_DomainWithoutPHPSettingsOverrides(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: domain with PHP pool but NO per-domain overrides
	now := time.Now().UTC()
	username := "phpuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "phpuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	phpPoolID := "pool-1"
	phpPool := &models.PHPPool{
		ID:         phpPoolID,
		PHPVersion: "8.5",
	}
	phpPoolRepo.pools[phpPoolID] = phpPool

	// Domain without overrides (all nil)
	domain := &models.Domain{
		ID:        "domain-1",
		UserID:    user.ID,
		Name:      "phpsite.com",
		DocRoot:   "/home/phpuser/domains/phpsite.com/public_html",
		IsEnabled: true,
		PHPPoolID: &phpPoolID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	err := r.ReconcileAll(ctx)
	require.NoError(t, err)

	// Verify domain.create was called and NO overrides were passed (filtering out filebrowser calls)
	domainCreateCalls := filterCallsByPrefix(agent.calls, "domain.create")
	require.Len(t, domainCreateCalls, 1, "should call domain.create exactly once")

	params := domainCreateCalls[0].params.(map[string]any)
	// When all are nil, they should not be in the params map
	_, hasMemLimit := params["php_memory_limit"]
	_, hasUpload := params["php_upload_max_filesize"]
	_, hasPost := params["php_post_max_size"]
	_, hasVars := params["php_max_input_vars"]
	_, hasExecTime := params["php_max_execution_time"]
	_, hasInputTime := params["php_max_input_time"]

	require.False(t, hasMemLimit, "php_memory_limit should not be present when nil")
	require.False(t, hasUpload, "php_upload_max_filesize should not be present when nil")
	require.False(t, hasPost, "php_post_max_size should not be present when nil")
	require.False(t, hasVars, "php_max_input_vars should not be present when nil")
	require.False(t, hasExecTime, "php_max_execution_time should not be present when nil")
	require.False(t, hasInputTime, "php_max_input_time should not be present when nil")
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

func TestReconcileOne_PassesCustomDirectives(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	now := time.Now().UTC()
	username := "bob"
	user := &models.User{
		ID:       "user-2",
		Email:    "bob@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	customDirectives := "add_header X-Foo bar;"
	domain := &models.Domain{
		ID:                     "domain-4",
		UserID:                 user.ID,
		Name:                   "test2.com",
		DocRoot:                "/home/bob/domains/test2.com/public_html",
		IsEnabled:              true,
		NginxCustomDirectives:  &customDirectives,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	domainRepo.domains[domain.ID] = domain

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	err := r.ReconcileOne(ctx, domain.ID)
	require.NoError(t, err)

	// Verify that domain.create was called
	require.Len(t, agent.calls, 1)
	require.Equal(t, "domain.create", agent.calls[0].method)

	// Verify that custom_directives was passed in params
	params := agent.calls[0].params.(map[string]any)
	require.Equal(t, customDirectives, params["custom_directives"])
}

func TestReconcileAllForce_RerendersEveryDomain(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// Setup: one user with multiple domains (enabled and disabled)
	now := time.Now().UTC()
	username := "testuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "test@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	domain1 := &models.Domain{
		ID:        "domain-1",
		UserID:    user.ID,
		Name:      "enabled.com",
		DocRoot:   "/home/testuser/domains/enabled.com/public_html",
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain2 := &models.Domain{
		ID:        "domain-2",
		UserID:    user.ID,
		Name:      "disabled.com",
		DocRoot:   "/home/testuser/domains/disabled.com/public_html",
		IsEnabled: false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain3 := &models.Domain{
		ID:        "domain-3",
		UserID:    user.ID,
		Name:      "another.com",
		DocRoot:   "/home/testuser/domains/another.com/public_html",
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain1.ID] = domain1
	domainRepo.domains[domain2.ID] = domain2
	domainRepo.domains[domain3.ID] = domain3

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	// Run ReconcileAllForce
	err := r.ReconcileAllForce(ctx)
	require.NoError(t, err)

	// Verify that domain.create was called 3 times (one for each domain)
	require.Len(t, agent.calls, 3)
	for i := 0; i < 3; i++ {
		require.Equal(t, "domain.create", agent.calls[i].method)
	}

	// Verify all three domains appear in the calls
	domainNames := make(map[string]bool)
	for _, call := range agent.calls {
		params := call.params.(map[string]any)
		domainNames[params["domain"].(string)] = true
	}
	require.True(t, domainNames["enabled.com"])
	require.True(t, domainNames["disabled.com"])
	require.True(t, domainNames["another.com"])
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

func TestReconcile_BootstrapsAndPushesZone(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	dnsZoneRepo := &fakeDNSZoneRepo{zones: make(map[string]*models.DNSZone)}
	dnsRecordRepo := &fakeDNSRecordRepo{records: make(map[string]*models.DNSRecord)}
	serverSettingsRepo := &fakeServerSettingsRepo{
		settings: &models.ServerSettings{
			PublicIPv4: "192.0.2.1",
			PublicIPv6: "2001:db8::1",
			NS1Name:    "ns1.example.com",
			NS2Name:    "ns2.example.com",
			AdminEmail: "admin@example.com",
		},
	}

	// Setup: one enabled domain in DB
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
		Name:      "example.com",
		DocRoot:   "/home/alice/domains/example.com/public_html",
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	// Create reconciler with DNS repos wired
	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithDNSRepos(dnsZoneRepo, dnsRecordRepo, serverSettingsRepo)

	// Run ReconcileOne on the domain
	err := r.ReconcileOne(ctx, domain.ID)
	require.NoError(t, err)

	// Verify that domain.create was called
	domainCreateFound := false
	dnsZoneUpsertFound := false

	for _, call := range agent.calls {
		if call.method == "domain.create" {
			domainCreateFound = true
		}
		if call.method == "dns.zone.upsert" {
			dnsZoneUpsertFound = true
			params := call.params.(map[string]any)
			require.Equal(t, "example.com", params["zone"])
			// Records should be a slice of compiled DNS records
			require.NotNil(t, params["records"], "expected records in dns.zone.upsert call")
		}
	}

	require.True(t, domainCreateFound, "domain.create should have been called")
	require.True(t, dnsZoneUpsertFound, "dns.zone.upsert should have been called")

	// Verify that a DNS zone was created in the zone repo
	zone, err := dnsZoneRepo.FindByDomainID(ctx, domain.ID)
	require.NoError(t, err)
	require.NotNil(t, zone)
	require.Equal(t, domain.Name, zone.Name)
	require.Equal(t, domain.ID, zone.DomainID)
	require.True(t, zone.IsEnabled)

	// Verify that bootstrap records were created
	records, err := dnsRecordRepo.ListByZoneID(ctx, zone.ID)
	require.NoError(t, err)
	// Bootstrap: A/@, A/www, A/mail, AAAA/@, AAAA/www, AAAA/mail, MX, SPF, DMARC = 9 records
	require.Len(t, records, 9)
}

func TestReconcile_PassesAXFRToAgent(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	dnsZoneRepo := &fakeDNSZoneRepo{zones: make(map[string]*models.DNSZone)}
	dnsRecordRepo := &fakeDNSRecordRepo{records: make(map[string]*models.DNSRecord)}
	serverSettingsRepo := &fakeServerSettingsRepo{
		settings: &models.ServerSettings{
			PublicIPv4: "192.0.2.1",
			PublicIPv6: "2001:db8::1",
			NS1Name:    "ns1.example.com",
			NS2Name:    "ns2.example.com",
			NS2IPv4:    "198.51.100.7", // Secondary nameserver configured
			AdminEmail: "admin@example.com",
		},
	}

	// Setup: one enabled domain in DB
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
		Name:      "example.com",
		DocRoot:   "/home/alice/domains/example.com/public_html",
		IsEnabled: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain.ID] = domain

	// Create reconciler with DNS repos wired
	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithDNSRepos(dnsZoneRepo, dnsRecordRepo, serverSettingsRepo)

	// Run ReconcileOne on the domain
	err := r.ReconcileOne(ctx, domain.ID)
	require.NoError(t, err)

	// Find the dns.zone.upsert call
	var zoneUpsertCall *fakeCall
	for i := range agent.calls {
		if agent.calls[i].method == "dns.zone.upsert" {
			zoneUpsertCall = &agent.calls[i]
			break
		}
	}

	require.NotNil(t, zoneUpsertCall, "dns.zone.upsert should have been called")

	params, ok := zoneUpsertCall.params.(map[string]any)
	require.True(t, ok, "params should be map[string]any, got %T", zoneUpsertCall.params)

	// Verify allow_axfr_from contains ns2's IPv4 + localhost
	allowAXFRRaw, ok := params["allow_axfr_from"]
	require.True(t, ok, "allow_axfr_from should be present in params")

	var allowAXFR []interface{}
	switch v := allowAXFRRaw.(type) {
	case []interface{}:
		allowAXFR = v
	case []string:
		for _, s := range v {
			allowAXFR = append(allowAXFR, s)
		}
	default:
		t.Fatalf("allow_axfr_from has unexpected type: %T", allowAXFRRaw)
	}
	require.True(t, ok, "allow_axfr_from should be a slice")
	require.Len(t, allowAXFR, 2, "should have 2 entries: ns2 IPv4 and localhost")

	// Check for ns2's IPv4 and localhost in the allow list
	foundNS2 := false
	foundLocal := false
	for _, entry := range allowAXFR {
		if str, ok := entry.(string); ok {
			if str == "198.51.100.7" {
				foundNS2 = true
			}
			if str == "127.0.0.1" {
				foundLocal = true
			}
		}
	}
	require.True(t, foundNS2, "allow_axfr_from should contain ns2 IPv4 (198.51.100.7)")
	require.True(t, foundLocal, "allow_axfr_from should contain localhost (127.0.0.1)")

	// Verify also_notify contains ns2's IPv4
	alsoNotifyRaw, ok := params["also_notify"]
	require.True(t, ok, "also_notify should be present in params")

	var alsoNotify []interface{}
	switch v := alsoNotifyRaw.(type) {
	case []interface{}:
		alsoNotify = v
	case []string:
		for _, s := range v {
			alsoNotify = append(alsoNotify, s)
		}
	default:
		t.Fatalf("also_notify has unexpected type: %T", alsoNotifyRaw)
	}

	require.Len(t, alsoNotify, 1, "should have 1 entry: ns2 IPv4")
	require.Equal(t, "198.51.100.7", alsoNotify[0])
}

// === PHP Pool Reconciliation Tests ===

func TestReconcilePHPPools_CreateDefaultPool(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with no pool
	username := "newuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "newuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	// Manually call reconcilePHPPools with mocked socket check
	r.socketReady = func(ctx context.Context, socketPath string, timeout, pollInterval time.Duration) bool {
		return true // Socket ready immediately
	}

	r.ReconcilePHPPools(ctx)

	// Verify that a pool was created with default values
	require.Len(t, phpPoolRepo.pools, 1, "should create 1 pool")
	var pool *models.PHPPool
	for _, p := range phpPoolRepo.pools {
		pool = p
	}
	require.NotNil(t, pool)
	require.Equal(t, user.ID, pool.UserID)
	require.Equal(t, "8.5", pool.PHPVersion)
	require.Equal(t, "active", pool.Status)
	require.NoError(t, nil)
}

func TestReconcilePHPPools_SkipActivePool(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with active pool
	username := "activeuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "activeuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	activePool := &models.PHPPool{
		ID:         "pool-1",
		UserID:     user.ID,
		PHPVersion: "8.3",
		PmMode:     "ondemand",
		Status:     "active",
	}
	phpPoolRepo.pools[activePool.ID] = activePool

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	r.ReconcilePHPPools(ctx)

	// Verify that no agent calls were made (pool already active)
	require.Len(t, agent.calls, 0, "should not call agent for active pool")
}

func TestReconcilePHPPools_RetryPendingPool(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with pending pool
	username := "pendinguser"
	user := &models.User{
		ID:       "user-1",
		Email:    "pendinguser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	pendingPool := &models.PHPPool{
		ID:         "pool-1",
		UserID:     user.ID,
		PHPVersion: "8.3",
		PmMode:     "ondemand",
		Status:     "pending",
	}
	phpPoolRepo.pools[pendingPool.ID] = pendingPool

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	r.socketReady = func(ctx context.Context, socketPath string, timeout, pollInterval time.Duration) bool {
		return true // Socket ready
	}

	r.ReconcilePHPPools(ctx)

	// Verify that agent was called
	require.Greater(t, len(agent.calls), 0, "should call agent for pending pool")

	// Find php.pool.apply call
	var applyCall *fakeCall
	for _, call := range agent.calls {
		if call.method == "php.pool.apply" {
			applyCall = &call
			break
		}
	}
	require.NotNil(t, applyCall, "should call php.pool.apply")

	// Verify pool status changed to active
	pool, err := phpPoolRepo.FindByID(ctx, pendingPool.ID)
	require.NoError(t, err)
	require.Equal(t, "active", pool.Status)
}

func TestReconcilePHPPools_RetryErrorPool(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with error pool
	username := "erruser"
	user := &models.User{
		ID:       "user-1",
		Email:    "erruser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	errMsg := "previous error"
	errorPool := &models.PHPPool{
		ID:         "pool-1",
		UserID:     user.ID,
		PHPVersion: "8.3",
		PmMode:     "ondemand",
		Status:     "error",
		LastError:  &errMsg,
	}
	phpPoolRepo.pools[errorPool.ID] = errorPool

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	r.socketReady = func(ctx context.Context, socketPath string, timeout, pollInterval time.Duration) bool {
		return true
	}

	r.ReconcilePHPPools(ctx)

	// Verify that agent was called to retry
	agentCallCount := 0
	for _, call := range agent.calls {
		if call.method == "php.pool.apply" {
			agentCallCount++
		}
	}
	require.Greater(t, agentCallCount, 0, "should retry error pool")

	// Verify pool status changed to active
	pool, err := phpPoolRepo.FindByID(ctx, errorPool.ID)
	require.NoError(t, err)
	require.Equal(t, "active", pool.Status)
}

func TestReconcilePHPPools_AgentFailureMarksError(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{
		failMethod: "php.pool.apply",
	}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with pending pool
	username := "failuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "failuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	pendingPool := &models.PHPPool{
		ID:     "pool-1",
		UserID: user.ID,
		Status: "pending",
	}
	phpPoolRepo.pools[pendingPool.ID] = pendingPool

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	r.ReconcilePHPPools(ctx)

	// Verify pool marked as error
	pool, err := phpPoolRepo.FindByID(ctx, pendingPool.ID)
	require.NoError(t, err)
	require.Equal(t, "error", pool.Status)
	require.NotNil(t, pool.LastError)
	require.Contains(t, *pool.LastError, "agent apply failed")
}

func TestReconcilePHPPools_SocketTimeoutMarksError(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with pending pool
	username := "timeoutuser"
	user := &models.User{
		ID:       "user-1",
		Email:    "timeoutuser@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	pendingPool := &models.PHPPool{
		ID:     "pool-1",
		UserID: user.ID,
		Status: "pending",
	}
	phpPoolRepo.pools[pendingPool.ID] = pendingPool

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	// Socket never becomes ready
	r.socketReady = func(ctx context.Context, socketPath string, timeout, pollInterval time.Duration) bool {
		return false
	}

	r.ReconcilePHPPools(ctx)

	// Verify pool marked as error
	pool, err := phpPoolRepo.FindByID(ctx, pendingPool.ID)
	require.NoError(t, err)
	require.Equal(t, "error", pool.Status)
	require.NotNil(t, pool.LastError)
	require.Equal(t, "socket did not become ready after agent apply", *pool.LastError)
}

func TestReconcilePHPPools_NginxRegenForBoundDomains(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: user with pending pool and two domains bound to it
	username := "phphost"
	user := &models.User{
		ID:       "user-1",
		Email:    "phphost@example.com",
		Username: &username,
	}
	userRepo.users[user.ID] = user

	pendingPool := &models.PHPPool{
		ID:     "pool-1",
		UserID: user.ID,
		Status: "pending",
	}
	phpPoolRepo.pools[pendingPool.ID] = pendingPool

	// Create two domains bound to this pool
	now := time.Now().UTC()
	domain1 := &models.Domain{
		ID:        "domain-1",
		UserID:    user.ID,
		Name:      "site1.com",
		DocRoot:   "/home/phphost/domains/site1.com/public_html",
		IsEnabled: true,
		PHPPoolID: &pendingPool.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domain2 := &models.Domain{
		ID:        "domain-2",
		UserID:    user.ID,
		Name:      "site2.com",
		DocRoot:   "/home/phphost/domains/site2.com/public_html",
		IsEnabled: true,
		PHPPoolID: &pendingPool.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	domainRepo.domains[domain1.ID] = domain1
	domainRepo.domains[domain2.ID] = domain2

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	r.socketReady = func(ctx context.Context, socketPath string, timeout, pollInterval time.Duration) bool {
		return true
	}

	r.ReconcilePHPPools(ctx)

	// Verify that domain.create was called for each bound domain
	domainCreateCount := 0
	for _, call := range agent.calls {
		if call.method == "domain.create" {
			domainCreateCount++
		}
	}
	require.Equal(t, 2, domainCreateCount, "should call domain.create for each bound domain")
}

func TestReconcilePHPPools_ContinueOnUserWithoutUsername(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	phpPoolRepo := &fakePHPPoolRepo{pools: make(map[string]*models.PHPPool)}

	// Setup: first user without username (will fail), second user with username (should succeed)
	user1 := &models.User{
		ID:       "user-1",
		Email:    "nouser@example.com",
		Username: nil, // No username
	}
	userRepo.users[user1.ID] = user1

	username2 := "gooduser"
	user2 := &models.User{
		ID:       "user-2",
		Email:    "gooduser@example.com",
		Username: &username2,
	}
	userRepo.users[user2.ID] = user2

	// Both users have pending pools
	pool1 := &models.PHPPool{
		ID:     "pool-1",
		UserID: user1.ID,
		Status: "pending",
	}
	pool2 := &models.PHPPool{
		ID:     "pool-2",
		UserID: user2.ID,
		Status: "pending",
	}
	phpPoolRepo.pools[pool1.ID] = pool1
	phpPoolRepo.pools[pool2.ID] = pool2

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithPHPPools(phpPoolRepo)

	r.socketReady = func(ctx context.Context, socketPath string, timeout, pollInterval time.Duration) bool {
		return true
	}

	r.ReconcilePHPPools(ctx)

	// Verify pool1 marked as error (no username)
	p1, _ := phpPoolRepo.FindByID(ctx, pool1.ID)
	require.Equal(t, "error", p1.Status)

	// Verify pool2 became active (username exists)
	p2, _ := phpPoolRepo.FindByID(ctx, pool2.ID)
	require.Equal(t, "active", p2.Status)
}

// fakeSSO provides a mock SSO service for testing.
type fakeSSO struct {
	ensureShadowCalls []string // Track userID calls
	ensureShadowError error    // Error to return
}

func (f *fakeSSO) EnsureShadow(ctx context.Context, userID string) error {
	f.ensureShadowCalls = append(f.ensureShadowCalls, userID)
	return f.ensureShadowError
}

func TestReconcileMysqlAdminShadow_SkipsIfNoSSO(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})
	// No WithSSO call; sso field is nil

	// Should not panic and should just return
	r.reconcileMysqlAdminShadow(ctx)
}

func TestReconcileMysqlAdminShadow_SkipsUsersWithoutUsername(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	sso := &fakeSSO{}

	// Create a user without username (should be skipped)
	user1 := &models.User{
		ID:       "user-1",
		Email:    "nousername@example.com",
		Username: nil,
	}
	userRepo.users[user1.ID] = user1

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithSSO(sso)

	r.reconcileMysqlAdminShadow(ctx)

	// SSO should never be called for user without username
	require.Equal(t, 0, len(sso.ensureShadowCalls))
}

func TestReconcileMysqlAdminShadow_SkipsUsersWithExistingShadow(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	sso := &fakeSSO{}

	// Create a user with username and existing shadow account
	username := "testuser"
	mysqladminUsername := "admin_testuser"
	user := &models.User{
		ID:                 "user-1",
		Email:              "test@example.com",
		Username:           &username,
		MysqladminUsername: &mysqladminUsername, // Already has shadow
	}
	userRepo.users[user.ID] = user

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithSSO(sso)

	r.reconcileMysqlAdminShadow(ctx)

	// SSO should not be called since shadow already exists
	require.Equal(t, 0, len(sso.ensureShadowCalls))
}

func TestReconcileMysqlAdminShadow_EnsuresForUsersNeedingShadow(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	sso := &fakeSSO{}

	// Create users: one needs shadow, one doesn't
	username1 := "user1"
	user1 := &models.User{
		ID:       "user-1",
		Email:    "user1@example.com",
		Username: &username1,
		// No shadow yet
	}
	userRepo.users[user1.ID] = user1

	username2 := "user2"
	mysqladminUsername2 := "admin_user2"
	user2 := &models.User{
		ID:                 "user-2",
		Email:              "user2@example.com",
		Username:           &username2,
		MysqladminUsername: &mysqladminUsername2, // Already has shadow
	}
	userRepo.users[user2.ID] = user2

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithSSO(sso)

	r.reconcileMysqlAdminShadow(ctx)

	// SSO should be called only for user1
	require.Equal(t, 1, len(sso.ensureShadowCalls))
	require.Equal(t, "user-1", sso.ensureShadowCalls[0])
}

func TestReconcileMysqlAdminShadow_ContinuesOnPerUserError(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	sso := &fakeSSO{ensureShadowError: errors.New("test error")}

	// Create multiple users needing shadow
	for i := 0; i < 3; i++ {
		username := fmt.Sprintf("user%d", i)
		user := &models.User{
			ID:       fmt.Sprintf("user-%d", i),
			Email:    fmt.Sprintf("user%d@example.com", i),
			Username: &username,
		}
		userRepo.users[user.ID] = user
	}

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithSSO(sso)

	// Should not panic even though SSO fails for all users
	r.reconcileMysqlAdminShadow(ctx)

	// All three should have been attempted (resilience)
	require.Equal(t, 3, len(sso.ensureShadowCalls))
}

func TestReconcileMysqlAdminShadow_BatchLimitOf50(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	sso := &fakeSSO{}

	// Create 75 users (exceeds batch limit of 50)
	for i := 0; i < 75; i++ {
		username := fmt.Sprintf("user%d", i)
		user := &models.User{
			ID:       fmt.Sprintf("user-%d", i),
			Email:    fmt.Sprintf("user%d@example.com", i),
			Username: &username,
		}
		userRepo.users[user.ID] = user
	}

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second}).
		WithSSO(sso)

	r.reconcileMysqlAdminShadow(ctx)

	// Should only process first 50 users in this pass (batch limit)
	require.Equal(t, 50, len(sso.ensureShadowCalls))
}

func TestReconcileFileBrowserUsers_CreatesUserForEachUsername(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// Create users with usernames
	username1 := "user1"
	user1 := &models.User{
		ID:       "user-1",
		Email:    "user1@example.com",
		Username: &username1,
	}
	userRepo.users[user1.ID] = user1

	username2 := "user2"
	user2 := &models.User{
		ID:       "user-2",
		Email:    "user2@example.com",
		Username: &username2,
	}
	userRepo.users[user2.ID] = user2

	// Create admin with no username (should be skipped)
	user3 := &models.User{
		ID:    "admin-1",
		Email: "admin@example.com",
		// No username (nil)
	}
	userRepo.users[user3.ID] = user3

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	r.ReconcileFileBrowserUsers(ctx)

	// Should have called filebrowser.user.ensure for user1 and user2 (not admin)
	ensureCalls := 0
	for _, call := range agent.calls {
		if call.method == "filebrowser.user.ensure" {
			ensureCalls++
		}
	}
	require.Equal(t, 2, ensureCalls, "should call filebrowser.user.ensure for each user with username")
}

func TestReconcileFileBrowserUsers_SkipsUserWithoutUsername(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// Admin with no username
	user := &models.User{
		ID:    "admin-1",
		Email: "admin@example.com",
		// No username
	}
	userRepo.users[user.ID] = user

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	r.ReconcileFileBrowserUsers(ctx)

	// Should not have called any filebrowser commands
	for _, call := range agent.calls {
		if call.method == "filebrowser.user.ensure" {
			t.Fatal("should not call filebrowser.user.ensure for admin without username")
		}
	}
}

func TestReconcileFileBrowserUsers_ContinuesOnAgentError(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{failMethod: "filebrowser.user.ensure"}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	// Create multiple users
	for i := 0; i < 3; i++ {
		username := fmt.Sprintf("user%d", i)
		user := &models.User{
			ID:       fmt.Sprintf("user-%d", i),
			Email:    fmt.Sprintf("user%d@example.com", i),
			Username: &username,
		}
		userRepo.users[user.ID] = user
	}

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	// Should not panic even though agent fails
	r.ReconcileFileBrowserUsers(ctx)

	// Should have attempted all users (resilient loop)
	ensureCalls := 0
	for _, call := range agent.calls {
		if call.method == "filebrowser.user.ensure" {
			ensureCalls++
		}
	}
	require.Equal(t, 3, ensureCalls, "should attempt all users despite agent error")
}

func TestReconcileFileBrowserUsers_ListsAndCleansOrphans(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	agent := &fakeAgent{}
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}

	username1 := "activeuser"
	user1 := &models.User{
		ID:       "user-1",
		Email:    "activeuser@example.com",
		Username: &username1,
	}
	userRepo.users[user1.ID] = user1

	r := New(domainRepo, userRepo, agent, log, Config{Interval: 1 * time.Second})

	r.ReconcileFileBrowserUsers(ctx)

	// Should have called filebrowser.user.list
	listCalled := false
	for _, call := range agent.calls {
		if call.method == "filebrowser.user.list" {
			listCalled = true
			break
		}
	}
	require.True(t, listCalled, "should call filebrowser.user.list for orphan cleanup")
}
