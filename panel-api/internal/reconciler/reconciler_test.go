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
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
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

	// Verify that domain.create was called (unified with is_enabled=false)
	require.Len(t, agent.calls, 2) // domain.list + domain.create
	require.Equal(t, "domain.list", agent.calls[0].method)
	require.Equal(t, "domain.create", agent.calls[1].method)
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
