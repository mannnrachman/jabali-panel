package pdns

import (
	"testing"
)

func TestValidateDomainName(t *testing.T) {
	cases := []struct {
		in    string
		valid bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"a.b.c.d", true},
		{"123123.com", true},
		{"mx.jabali-panel.com", true},
		{"EXAMPLE.com", false},             // uppercase rejected
		{"example", false},                 // single label
		{"-example.com", false},            // leading hyphen
		{"example-.com", false},            // trailing hyphen
		{"exa..mple.com", false},           // double dot
		{"example.com; rm -rf /", false},   // shell injection attempt
		{"../../etc/passwd", false},        // path traversal
		{"example.com\nanother", false},    // newline
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			err := validateDomainName(c.in)
			got := err == nil
			if got != c.valid {
				t.Fatalf("validateDomainName(%q): got valid=%v (err=%v), want %v", c.in, got, err, c.valid)
			}
		})
	}
}

const showZoneSigned = `
This is a Zone secured with: NSEC3
Metadata items:
	NSEC3PARAM 1 0 10 ab
keys:
ID = 1 (KSK), flags = 257, tag = 34567, algo = 13, bits = 256	Active: 1 ( ECDSAP256SHA256 )
KSK DNSKEY = example.com. IN DNSKEY 257 3 13 base64key1== ;
DS = example.com. IN DS 34567 13 2 abcdef1234567890 ; ( SHA256 digest )

ID = 2 (ZSK), flags = 256, tag = 12345, algo = 13, bits = 256	Active: 1 ( ECDSAP256SHA256 )
ZSK DNSKEY = example.com. IN DNSKEY 256 3 13 base64key2== ;
`

const showZoneUnsigned = `
Zone example.com. is not actively secured
Metadata items: None
keys:
`

const showZoneWithInactive = `
keys:
ID = 1 (KSK), flags = 257, tag = 34567, algo = 13, bits = 256	Active: 0 ( ECDSAP256SHA256 )
KSK DNSKEY = example.com. IN DNSKEY 257 3 13 pending== ;
ID = 2 (KSK), flags = 257, tag = 77777, algo = 13, bits = 256	Active: 1 ( ECDSAP256SHA256 )
KSK DNSKEY = example.com. IN DNSKEY 257 3 13 active== ;
`

func TestParseShowZone_Signed(t *testing.T) {
	keys := parseShowZone(showZoneSigned)
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d: %+v", len(keys), keys)
	}
	ksk := keys[0]
	if ksk.KeyType != "KSK" || ksk.KeyTag != 34567 || ksk.Algorithm != 13 || !ksk.Active {
		t.Errorf("KSK unexpected: %+v", ksk)
	}
	if ksk.PublicKey == "" {
		t.Errorf("KSK missing DNSKEY")
	}
	zsk := keys[1]
	if zsk.KeyType != "ZSK" || zsk.KeyTag != 12345 {
		t.Errorf("ZSK unexpected: %+v", zsk)
	}
}

func TestParseShowZone_Unsigned(t *testing.T) {
	keys := parseShowZone(showZoneUnsigned)
	if len(keys) != 0 {
		t.Fatalf("unsigned zone must yield no keys, got %d: %+v", len(keys), keys)
	}
}

func TestParseShowZone_RolloverPending(t *testing.T) {
	keys := parseShowZone(showZoneWithInactive)
	if len(keys) != 2 {
		t.Fatalf("want 2 keys (one pending, one active), got %d", len(keys))
	}
	if keys[0].Active != false {
		t.Errorf("first KSK should be inactive, got Active=%v", keys[0].Active)
	}
	if keys[1].Active != true {
		t.Errorf("second KSK should be active, got Active=%v", keys[1].Active)
	}
}

const exportZoneDSSingle = `example.com. IN DS 34567 13 2 ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890
`

const exportZoneDSMulti = `example.com. IN DS 34567 13 1 abc123
example.com. IN DS 34567 13 2 def456
example.com. IN DS 34567 13 4 cafe89
`

const exportZoneDSEmpty = ``

func TestParseExportZoneDS_Single(t *testing.T) {
	records := parseExportZoneDS(exportZoneDSSingle)
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	r := records[0]
	if r.KeyTag != 34567 || r.Algorithm != 13 || r.DigestType != 2 {
		t.Errorf("unexpected record: %+v", r)
	}
	if r.Digest != "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890" {
		t.Errorf("digest not lowercased: %q", r.Digest)
	}
}

func TestParseExportZoneDS_Multi(t *testing.T) {
	records := parseExportZoneDS(exportZoneDSMulti)
	if len(records) != 3 {
		t.Fatalf("want 3 DS records, got %d", len(records))
	}
	wantTypes := []uint8{1, 2, 4}
	for i, r := range records {
		if r.DigestType != wantTypes[i] {
			t.Errorf("record %d: want digest type %d, got %d", i, wantTypes[i], r.DigestType)
		}
	}
}

func TestParseExportZoneDS_Empty(t *testing.T) {
	records := parseExportZoneDS(exportZoneDSEmpty)
	if len(records) != 0 {
		t.Fatalf("empty output must yield 0 records, got %d", len(records))
	}
}
