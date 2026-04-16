package dnscompile

import (
	"strings"
	"testing"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestCompile_EmptyRecords(t *testing.T) {
	zone := &models.DNSZone{
		ID:             "zone1",
		DomainID:       "dom1",
		Name:           "example.com",
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	srv := &models.ServerSettings{
		PublicIPv4: "192.0.2.1",
		NS1Name:    "ns1.example.com",
	}

	result := Compile(zone, []models.DNSRecord{}, srv)

	if len(result) < 2 {
		t.Fatalf("expected at least 2 records (SOA + NS), got %d", len(result))
	}

	if result[0].Type != "SOA" {
		t.Errorf("first record should be SOA, got %s", result[0].Type)
	}

	hasNS := false
	for _, r := range result {
		if r.Type == "NS" && r.Content == "ns1.example.com" {
			hasNS = true
			break
		}
	}
	if !hasNS {
		t.Error("expected NS record for ns1.example.com")
	}
}

func TestCompile_NS1Only(t *testing.T) {
	zone := &models.DNSZone{
		ID:             "zone1",
		DomainID:       "dom1",
		Name:           "example.com",
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	srv := &models.ServerSettings{
		NS1Name: "ns1.example.com",
		NS2Name: "",
	}

	result := Compile(zone, []models.DNSRecord{}, srv)

	nsCount := 0
	for _, r := range result {
		if r.Type == "NS" {
			nsCount++
		}
	}
	if nsCount != 1 {
		t.Errorf("expected 1 NS record, got %d", nsCount)
	}
}

func TestCompile_AdminEmailToHostmaster(t *testing.T) {
	zone := &models.DNSZone{
		ID:             "zone1",
		DomainID:       "dom1",
		Name:           "example.com",
		RefreshSeconds: 3600,
		RetrySeconds:   600,
		ExpireSeconds:  604800,
		MinimumTTL:     3600,
		IsEnabled:      true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	srv := &models.ServerSettings{
		AdminEmail: "admin@example.com",
		NS1Name:    "ns1.example.com",
	}

	result := Compile(zone, []models.DNSRecord{}, srv)

	for _, r := range result {
		if r.Type == "SOA" {
			if !strings.Contains(r.Content, "admin.example.com") {
				t.Errorf("SOA should contain hostmaster, got %s", r.Content)
			}
			return
		}
	}
	t.Error("did not find SOA record")
}

func TestExpandName_AtSymbol(t *testing.T) {
	result := expandName("@", "example.com")
	if result != "example.com" {
		t.Errorf("@ should expand to example.com, got %s", result)
	}
}

func TestExpandName_ShortLabel(t *testing.T) {
	result := expandName("www", "example.com")
	if result != "www.example.com" {
		t.Errorf("www should expand to www.example.com, got %s", result)
	}
}

func TestExpandName_FQDNWithTrailingDot(t *testing.T) {
	result := expandName("www.other.com.", "example.com")
	if result != "www.other.com" {
		t.Errorf("trailing dot should be stripped, got %s", result)
	}
}

func TestEmailToSOAHostmaster_Simple(t *testing.T) {
	result := emailToSOAHostmaster("admin@example.com")
	expected := "admin.example.com"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestEmailToSOAHostmaster_DotsInLocalPart(t *testing.T) {
	result := emailToSOAHostmaster("john.doe@example.com")
	expected := `john\.doe.example.com`
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}
