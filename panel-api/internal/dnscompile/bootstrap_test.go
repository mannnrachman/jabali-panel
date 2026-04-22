package dnscompile

import (
	"strconv"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// idCounter returns a deterministic id-generator for tests. Keeps assertions
// about record ordering readable without coupling to ULID time bits.
func bootIDCounter() func() string {
	n := 0
	return func() string {
		n++
		return "id-" + strconv.Itoa(n)
	}
}

// Find returns the first record matching (name,type). Fails the test if
// absent — every lookup in the bootstrap tests is load-bearing.
func findRec(t *testing.T, recs []models.DNSRecord, name, typ string) models.DNSRecord {
	t.Helper()
	for _, r := range recs {
		if r.Name == name && r.Type == typ {
			return r
		}
	}
	t.Fatalf("no record found for name=%q type=%q; got %d records", name, typ, len(recs))
	return models.DNSRecord{}
}

func TestBootstrapRecords_WWWIsCNAMEToApex(t *testing.T) {
	recs := BootstrapRecords(
		"zone1",
		"example.com",
		&models.ServerSettings{PublicIPv4: "192.0.2.1"},
		bootIDCounter(),
	)

	// www must be a CNAME, not an A — MX-target-can't-be-a-CNAME
	// consideration doesn't apply here (www isn't an MX target), and
	// the shape lets apex-IP changes propagate without rewrites.
	for _, r := range recs {
		if r.Name == "www" && r.Type == "A" {
			t.Fatalf("www must not have an A record after the CNAME migration; got %+v", r)
		}
	}
	www := findRec(t, recs, "www", "CNAME")
	if www.Content != "example.com" {
		t.Errorf("www CNAME content should be the apex FQDN, got %q", www.Content)
	}
}

func TestBootstrapRecords_MailStaysA_NotCNAME(t *testing.T) {
	// RFC 2181 §10.3: MX targets MUST NOT be CNAME aliases. The MX
	// record below points at "mail", so "mail" must be an A (+AAAA
	// when v6 is set), never a CNAME.
	recs := BootstrapRecords(
		"zone1",
		"example.com",
		&models.ServerSettings{PublicIPv4: "192.0.2.1", PublicIPv6: "2001:db8::1"},
		bootIDCounter(),
	)
	for _, r := range recs {
		if r.Name == "mail" && r.Type == "CNAME" {
			t.Fatalf("mail must not be a CNAME (RFC 2181 §10.3 — MX targets can't be aliases); got %+v", r)
		}
	}
	a := findRec(t, recs, "mail", "A")
	if a.Content != "192.0.2.1" {
		t.Errorf("mail A content wrong: %q", a.Content)
	}
	aaaa := findRec(t, recs, "mail", "AAAA")
	if aaaa.Content != "2001:db8::1" {
		t.Errorf("mail AAAA content wrong: %q", aaaa.Content)
	}
}

func TestBootstrapRecords_SPFIncludesIP4AndIP6(t *testing.T) {
	tests := []struct {
		name   string
		v4, v6 string
		want   string
	}{
		{"v4 only", "192.0.2.1", "", `"v=spf1 mx ip4:192.0.2.1 ~all"`},
		{"v4 and v6", "192.0.2.1", "2001:db8::1", `"v=spf1 mx ip4:192.0.2.1 ip6:2001:db8::1 ~all"`},
		{"neither", "", "", `"v=spf1 mx ~all"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs := BootstrapRecords(
				"zone1",
				"example.com",
				&models.ServerSettings{PublicIPv4: tt.v4, PublicIPv6: tt.v6},
				bootIDCounter(),
			)
			// SPF is the "@" TXT that starts with v=spf1.
			var spf string
			for _, r := range recs {
				if r.Name == "@" && r.Type == "TXT" && strings.Contains(r.Content, "v=spf1") {
					spf = r.Content
					break
				}
			}
			if spf == "" {
				t.Fatal("no SPF record found")
			}
			if spf != tt.want {
				t.Errorf("SPF mismatch:\n  want: %s\n  got:  %s", tt.want, spf)
			}
		})
	}
}

func TestBootstrapRecords_NoServerSettingsReturnsEmpty(t *testing.T) {
	recs := BootstrapRecords("zone1", "example.com", nil, bootIDCounter())
	if len(recs) != 0 {
		t.Errorf("expected 0 records when srv is nil, got %d", len(recs))
	}
}

func TestBootstrapRecords_WWWSkippedWhenZoneNameEmpty(t *testing.T) {
	// Safety: if the caller somehow forgets to pass zoneName, we would
	// rather emit no www record than write an empty-content CNAME.
	recs := BootstrapRecords(
		"zone1",
		"",
		&models.ServerSettings{PublicIPv4: "192.0.2.1"},
		bootIDCounter(),
	)
	for _, r := range recs {
		if r.Name == "www" {
			t.Fatalf("www record must not be emitted when zoneName is empty; got %+v", r)
		}
	}
}

func TestBootstrapRecords_AllManagedTrue_ManagedByNil(t *testing.T) {
	// Contract: Managed=true lets the UI mark the rows read-only;
	// ManagedBy=nil is what keeps the email-disable cleanup
	// (WHERE managed_by="m6") from touching them.
	recs := BootstrapRecords(
		"zone1",
		"example.com",
		&models.ServerSettings{PublicIPv4: "192.0.2.1"},
		bootIDCounter(),
	)
	for _, r := range recs {
		if !r.Managed {
			t.Errorf("record %s %s should be Managed=true", r.Name, r.Type)
		}
		if r.ManagedBy != nil {
			t.Errorf("record %s %s should have ManagedBy=nil, got %v", r.Name, r.Type, *r.ManagedBy)
		}
	}
}
