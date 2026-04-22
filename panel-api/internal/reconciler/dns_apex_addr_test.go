package reconciler

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func newReconcilerForApexTest(zone *models.DNSZone, ips *fakeManagedIPs) (*Reconciler, *fakeDNSRecordRepo) {
	dnsRepo := &fakeDNSRecordRepo{records: make(map[string]*models.DNSRecord)}
	zoneRepo := &fakeDNSZoneRepo{zones: map[string]*models.DNSZone{zone.ID: zone}}
	r := &Reconciler{
		dnsZones:   zoneRepo,
		dnsRecords: dnsRepo,
		managedIPs: ips,
		log:        slog.Default(),
	}
	return r, dnsRepo
}

// TestConvergeApex_NoBindingUsesServerDefault — domain with NULL listen
// IDs picks up the family default's address.
func TestConvergeApex_NoBindingUsesServerDefault(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	ips := &fakeManagedIPs{rows: []models.ManagedIP{
		{ID: 1, Address: "192.0.2.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "2001:db8::1", Family: "ipv6", IsDefault: true},
	}}
	r, dnsRepo := newReconcilerForApexTest(zone, ips)
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1"})

	v4, v6 := findApex(dnsRepo, "A"), findApex(dnsRepo, "AAAA")
	if v4 == nil || v4.Content != "192.0.2.1" {
		t.Errorf("expected @ A 192.0.2.1, got %+v", v4)
	}
	if v6 == nil || v6.Content != "2001:db8::1" {
		t.Errorf("expected @ AAAA 2001:db8::1, got %+v", v6)
	}
}

// TestConvergeApex_BindingOverridesDefault — domain pinned to a
// non-default IPv4 picks up THAT address, not the server primary.
func TestConvergeApex_BindingOverridesDefault(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	ips := &fakeManagedIPs{rows: []models.ManagedIP{
		{ID: 1, Address: "192.0.2.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
	}}
	r, dnsRepo := newReconcilerForApexTest(zone, ips)
	id := uint64(2)
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1", ListenIPv4ID: &id})

	v4 := findApex(dnsRepo, "A")
	if v4 == nil || v4.Content != "203.0.113.99" {
		t.Errorf("expected @ A 203.0.113.99 (binding wins), got %+v", v4)
	}
}

// TestConvergeApex_UpdatesDriftedManagedRow — pre-existing managed row
// with stale content gets rewritten when the binding changes.
func TestConvergeApex_UpdatesDriftedManagedRow(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	ips := &fakeManagedIPs{rows: []models.ManagedIP{
		{ID: 1, Address: "192.0.2.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
	}}
	r, dnsRepo := newReconcilerForApexTest(zone, ips)

	// Pre-seed a managed apex A pointing at the old (server primary) IP.
	dnsRepo.records["existing"] = &models.DNSRecord{
		ID: "existing", ZoneID: zone.ID, Name: "@", Type: "A",
		Content: "192.0.2.1", Managed: true, ManagedBy: nil,
		IsEnabled: true, CreatedAt: time.Now(),
	}

	id := uint64(2)
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1", ListenIPv4ID: &id})

	v4 := findApex(dnsRepo, "A")
	if v4 == nil || v4.Content != "203.0.113.99" {
		t.Errorf("managed row should have been updated to 203.0.113.99; got %+v", v4)
	}
	// Same row mutated in place — no second @ A row created.
	if count := countApex(dnsRepo, "A"); count != 1 {
		t.Errorf("expected exactly 1 @ A row, got %d", count)
	}
}

// TestConvergeApex_DoesNotTouchUserEditedRow — Managed=false row is
// the operator's edit; we must NEVER overwrite it.
func TestConvergeApex_DoesNotTouchUserEditedRow(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	ips := &fakeManagedIPs{rows: []models.ManagedIP{
		{ID: 1, Address: "192.0.2.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
	}}
	r, dnsRepo := newReconcilerForApexTest(zone, ips)

	dnsRepo.records["userrow"] = &models.DNSRecord{
		ID: "userrow", ZoneID: zone.ID, Name: "@", Type: "A",
		Content: "10.0.0.42", Managed: false, ManagedBy: nil,
		IsEnabled: true, CreatedAt: time.Now(),
	}

	id := uint64(2)
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1", ListenIPv4ID: &id})

	v4 := findApex(dnsRepo, "A")
	if v4 == nil || v4.Content != "10.0.0.42" {
		t.Errorf("user-edited row must NOT be overwritten; got %+v", v4)
	}
}

// TestConvergeApex_DoesNotTouchM6OwnedRow — ManagedBy="m6" is the M6
// email subsystem's territory; convergence must skip those rows.
func TestConvergeApex_DoesNotTouchM6OwnedRow(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	ips := &fakeManagedIPs{rows: []models.ManagedIP{
		{ID: 1, Address: "192.0.2.1", Family: "ipv4", IsDefault: true},
		{ID: 2, Address: "203.0.113.99", Family: "ipv4"},
	}}
	r, dnsRepo := newReconcilerForApexTest(zone, ips)

	dnsRepo.records["m6row"] = &models.DNSRecord{
		ID: "m6row", ZoneID: zone.ID, Name: "@", Type: "A",
		Content: "10.0.0.5", Managed: true, ManagedBy: stringPtr("m6"),
		IsEnabled: true, CreatedAt: time.Now(),
	}

	id := uint64(2)
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1", ListenIPv4ID: &id})

	v4 := findApex(dnsRepo, "A")
	if v4 == nil || v4.Content != "10.0.0.5" {
		t.Errorf("M6-owned row must NOT be overwritten; got %+v", v4)
	}
}

// TestConvergeApex_NoIPv6PoolNoIPv6Row — no v6 default + no v6 binding
// means the convergence skips creating a v6 row (avoids leaving an
// empty AAAA in the zone).
func TestConvergeApex_NoIPv6PoolNoIPv6Row(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	ips := &fakeManagedIPs{rows: []models.ManagedIP{
		{ID: 1, Address: "192.0.2.1", Family: "ipv4", IsDefault: true},
		// no v6 default seeded
	}}
	r, dnsRepo := newReconcilerForApexTest(zone, ips)
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1"})

	if v6 := findApex(dnsRepo, "AAAA"); v6 != nil {
		t.Errorf("expected no @ AAAA row when v6 unconfigured; got %+v", v6)
	}
	// v4 still happens.
	if v4 := findApex(dnsRepo, "A"); v4 == nil || v4.Content != "192.0.2.1" {
		t.Errorf("v4 row missing despite default seeded: %+v", v4)
	}
}

// TestConvergeApex_NoManagedIPsRepoIsNoOp — without the IP-pool repo,
// the function shouldn't touch anything (older deployments).
func TestConvergeApex_NoManagedIPsRepoIsNoOp(t *testing.T) {
	zone := &models.DNSZone{ID: "z1", Name: "example.com"}
	dnsRepo := &fakeDNSRecordRepo{records: make(map[string]*models.DNSRecord)}
	r := &Reconciler{
		dnsRecords: dnsRepo,
		log:        slog.Default(),
	}
	r.convergeApexAddrRecords(context.Background(), zone, &models.Domain{ID: "d1"})
	if len(dnsRepo.records) != 0 {
		t.Errorf("no managedIPs ⇒ no DNS writes; got %d records", len(dnsRepo.records))
	}
}

// findApex returns the first @-named record of the given type, or nil.
func findApex(repo *fakeDNSRecordRepo, recType string) *models.DNSRecord {
	for _, r := range repo.records {
		if r.Name == "@" && r.Type == recType {
			return r
		}
	}
	return nil
}

func countApex(repo *fakeDNSRecordRepo, recType string) int {
	n := 0
	for _, r := range repo.records {
		if r.Name == "@" && r.Type == recType {
			n++
		}
	}
	return n
}
