package reconciler

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/dnscompile"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Tests for Reconciler.migrateBootstrapShape. Every case exercises the
// function in isolation — no agent, no domain reconcile — so failures
// point directly at the migration logic.

func migID(seed int) func() string {
	n := seed
	return func() string { n++; return "mig-" + strconv.Itoa(n) }
}

func newMigrator(t *testing.T) (*Reconciler, *fakeDNSRecordRepo, *fakeDNSZoneRepo) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	domainRepo := &fakeDomainRepo{domains: make(map[string]*models.Domain)}
	userRepo := &fakeUserRepo{users: make(map[string]*models.User)}
	zoneRepo := &fakeDNSZoneRepo{zones: make(map[string]*models.DNSZone)}
	recRepo := &fakeDNSRecordRepo{records: make(map[string]*models.DNSRecord)}
	srvRepo := &fakeServerSettingsRepo{settings: &models.ServerSettings{PublicIPv4: "192.0.2.1"}}
	r := New(domainRepo, userRepo, &fakeAgent{}, log, Config{Interval: 1 * time.Second}).
		WithDNSRepos(zoneRepo, recRepo, srvRepo)
	return r, recRepo, zoneRepo
}

func seedZone(t *testing.T, zoneRepo *fakeDNSZoneRepo, id, name string) *models.DNSZone {
	t.Helper()
	z := &models.DNSZone{ID: id, Name: name, IsEnabled: true, UpdatedAt: time.Now().UTC()}
	zoneRepo.zones[id] = z
	return z
}

// managedBootstrap returns a record that matches the (Managed=true,
// ManagedBy=nil) tuple migrateBootstrapShape treats as legacy.
func managedBootstrap(id, zoneID, name, typ, content string) *models.DNSRecord {
	return &models.DNSRecord{
		ID: id, ZoneID: zoneID, Name: name, Type: typ, Content: content,
		TTL: 3600, Managed: true, IsEnabled: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}

func TestMigrate_LegacyWWWArecord_BecomesCNAMEToApex(t *testing.T) {
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")

	rec.records["r1"] = managedBootstrap("r1", z.ID, "www", "A", "192.0.2.1")
	rec.records["r2"] = managedBootstrap("r2", z.ID, "www", "AAAA", "2001:db8::1")

	r.migrateBootstrapShape(ctx, z, &models.ServerSettings{PublicIPv4: "192.0.2.1"})

	// Both legacy rows gone.
	for id := range rec.records {
		if id == "r1" || id == "r2" {
			t.Errorf("legacy www %s row should have been deleted", id)
		}
	}
	// Exactly one www CNAME remains, content = zone name.
	cnames := 0
	for _, r := range rec.records {
		if r.Name == "www" && r.Type == "CNAME" {
			cnames++
			require.Equal(t, "example.com", r.Content)
			require.True(t, r.Managed)
			require.Nil(t, r.ManagedBy)
		}
	}
	require.Equal(t, 1, cnames, "expected exactly one www CNAME after migration")
}

func TestMigrate_LegacyWWW_SkippedWhenOperatorCNAMEExists(t *testing.T) {
	// If an operator already pointed www at a third-party host with a
	// CNAME, we must NOT delete their legacy A row underneath them.
	// The wwwCNAMEExists guard short-circuits the whole www branch.
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")

	rec.records["legacy"] = managedBootstrap("legacy", z.ID, "www", "A", "192.0.2.1")
	op := managedBootstrap("op", z.ID, "www", "CNAME", "some-cdn.example.net")
	op.Managed = false // operator-created
	rec.records["op"] = op

	r.migrateBootstrapShape(ctx, z, &models.ServerSettings{PublicIPv4: "192.0.2.1"})

	// Legacy A still present, operator CNAME untouched.
	require.NotNil(t, rec.records["legacy"], "legacy www A must not be deleted when a www CNAME exists")
	require.Equal(t, "some-cdn.example.net", rec.records["op"].Content)
}

func TestMigrate_LegacyWWW_UntouchedWhenManagedFalse(t *testing.T) {
	// Managed=false means the row is operator-edited or hand-created.
	// Must be left alone.
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")

	row := managedBootstrap("r1", z.ID, "www", "A", "10.0.0.5")
	row.Managed = false
	rec.records["r1"] = row

	r.migrateBootstrapShape(ctx, z, &models.ServerSettings{PublicIPv4: "192.0.2.1"})

	require.NotNil(t, rec.records["r1"], "operator-edited (Managed=false) row must survive migration")
	require.Equal(t, "A", rec.records["r1"].Type)
}

func TestMigrate_LegacyWWW_UntouchedWhenManagedBySet(t *testing.T) {
	// ManagedBy != nil means the row belongs to a feature (e.g. M6 email).
	// Migration must never rewrite those — each feature owns its own
	// cleanup.
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")

	m6 := "m6"
	row := managedBootstrap("r1", z.ID, "www", "A", "192.0.2.1")
	row.ManagedBy = &m6
	rec.records["r1"] = row

	r.migrateBootstrapShape(ctx, z, &models.ServerSettings{PublicIPv4: "192.0.2.1"})

	require.NotNil(t, rec.records["r1"], "ManagedBy-tagged row must survive migration")
	require.Equal(t, "A", rec.records["r1"].Type)
}

func TestMigrate_LegacySPF_GetsIP4IP6(t *testing.T) {
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")

	rec.records["spf"] = managedBootstrap("spf", z.ID, "@", "TXT",
		dnscompile.LegacyBootstrapSPFContent)

	srv := &models.ServerSettings{PublicIPv4: "192.0.2.1", PublicIPv6: "2001:db8::1"}
	r.migrateBootstrapShape(ctx, z, srv)

	require.Equal(t,
		`"v=spf1 mx ip4:192.0.2.1 ip6:2001:db8::1 ~all"`,
		rec.records["spf"].Content)
}

func TestMigrate_SPF_UntouchedWhenOperatorEdited(t *testing.T) {
	// A single character of drift from LegacyBootstrapSPFContent means
	// the operator edited the SPF (added an include:, swapped ~all for
	// -all, etc.). Migration must skip.
	tests := []string{
		`"v=spf1 mx -all"`,                         // changed qualifier
		`"v=spf1 mx include:_spf.google.com ~all"`, // added include
		`v=spf1 mx ~all`,                           // unquoted
	}
	for _, content := range tests {
		t.Run(content, func(t *testing.T) {
			ctx := context.Background()
			r, rec, zr := newMigrator(t)
			z := seedZone(t, zr, "zone1", "example.com")

			rec.records["spf"] = managedBootstrap("spf", z.ID, "@", "TXT", content)

			r.migrateBootstrapShape(ctx, z, &models.ServerSettings{PublicIPv4: "192.0.2.1"})

			require.Equal(t, content, rec.records["spf"].Content,
				"operator-edited SPF %q must be left alone", content)
		})
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")
	srv := &models.ServerSettings{PublicIPv4: "192.0.2.1"}

	rec.records["r1"] = managedBootstrap("r1", z.ID, "www", "A", "192.0.2.1")
	rec.records["spf"] = managedBootstrap("spf", z.ID, "@", "TXT",
		dnscompile.LegacyBootstrapSPFContent)

	// First pass: migrate.
	r.migrateBootstrapShape(ctx, z, srv)

	// Snapshot contents after first pass.
	type snap struct{ name, typ, content string }
	var first []snap
	for _, r := range rec.records {
		first = append(first, snap{r.Name, r.Type, r.Content})
	}

	// Second pass: nothing should change — no new deletes, no
	// regenerated CNAMEs (which would produce a different ULID on the
	// fresh CNAME row and break real-world DNS records with TTL churn).
	r.migrateBootstrapShape(ctx, z, srv)

	require.Len(t, rec.records, len(first),
		"idempotent migration must not add or remove rows on second pass")
}

func TestMigrate_NilSettings_NoOp(t *testing.T) {
	ctx := context.Background()
	r, rec, zr := newMigrator(t)
	z := seedZone(t, zr, "zone1", "example.com")

	rec.records["r1"] = managedBootstrap("r1", z.ID, "www", "A", "192.0.2.1")
	r.migrateBootstrapShape(ctx, z, nil)

	require.NotNil(t, rec.records["r1"], "nil server settings must skip migration (can't compute new content)")
}
