package dmarcrua

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// Real-shape sample condensed from a Google-issued DMARC RUA report.
const sampleXML = `<?xml version="1.0" encoding="UTF-8"?>
<feedback>
  <report_metadata>
    <org_name>google.com</org_name>
    <email>noreply-dmarc-support@google.com</email>
    <report_id>1234567890</report_id>
    <date_range>
      <begin>1719964800</begin>
      <end>1720051199</end>
    </date_range>
  </report_metadata>
  <policy_published>
    <domain>example.com</domain>
    <adkim>r</adkim>
    <aspf>r</aspf>
    <p>none</p>
    <pct>100</pct>
  </policy_published>
  <record>
    <row>
      <source_ip>198.51.100.10</source_ip>
      <count>15</count>
      <policy_evaluated>
        <disposition>none</disposition>
        <dkim>pass</dkim>
        <spf>pass</spf>
      </policy_evaluated>
    </row>
    <identifiers><header_from>example.com</header_from></identifiers>
  </record>
  <record>
    <row>
      <source_ip>203.0.113.7</source_ip>
      <count>3</count>
      <policy_evaluated>
        <disposition>quarantine</disposition>
        <dkim>fail</dkim>
        <spf>fail</spf>
      </policy_evaluated>
    </row>
    <identifiers><header_from>example.com</header_from></identifiers>
  </record>
</feedback>
`

func TestParse_PlainXML(t *testing.T) {
	r, err := Parse([]byte(sampleXML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Reporter != "google.com" {
		t.Errorf("reporter = %q", r.Reporter)
	}
	if r.Domain != "example.com" {
		t.Errorf("domain = %q", r.Domain)
	}
	if r.WindowStart.Unix() != 1719964800 || r.WindowEnd.Unix() != 1720051199 {
		t.Errorf("window: %v..%v", r.WindowStart, r.WindowEnd)
	}
	if len(r.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(r.Rows))
	}
	r0 := r.Rows[0]
	if r0.SourceIP != "198.51.100.10" || r0.Cnt != 15 ||
		r0.Disposition != "none" || r0.DKIM != "pass" || r0.SPF != "pass" {
		t.Errorf("row[0] = %+v", r0)
	}
	r1 := r.Rows[1]
	if r1.SourceIP != "203.0.113.7" || r1.Cnt != 3 ||
		r1.Disposition != "quarantine" || r1.DKIM != "fail" || r1.SPF != "fail" {
		t.Errorf("row[1] = %+v", r1)
	}
	// All rows must propagate domain/reporter/window for the repo
	// idempotency tuple.
	for i, row := range r.Rows {
		if row.Domain != "example.com" || row.Reporter != "google.com" {
			t.Errorf("row[%d] domain/reporter not propagated: %+v", i, row)
		}
	}
}

func TestParse_Gzip(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(sampleXML))
	gz.Close()
	r, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatalf("parse gzip: %v", err)
	}
	if r.Reporter != "google.com" || len(r.Rows) != 2 {
		t.Errorf("gzip-parsed report missing: %+v", r)
	}
}

func TestParse_Zip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("report.xml")
	f.Write([]byte(sampleXML))
	zw.Close()
	r, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatalf("parse zip: %v", err)
	}
	if r.Reporter != "google.com" || len(r.Rows) != 2 {
		t.Errorf("zip-parsed report missing: %+v", r)
	}
}

func TestParse_RejectsMalformed(t *testing.T) {
	if _, err := Parse([]byte("<not-a-feedback/>")); err == nil {
		t.Error("malformed XML should error")
	}
	if _, err := Parse([]byte("<feedback><report_metadata/><policy_published/></feedback>")); err == nil ||
		!strings.Contains(err.Error(), "missing report_metadata.org_name or policy_published.domain") {
		t.Errorf("missing required fields should error, got: %v", err)
	}
}

func TestParse_EmptyDispositionDefaultsToNone(t *testing.T) {
	xml := `<feedback>
  <report_metadata><org_name>x</org_name><date_range><begin>1</begin><end>2</end></date_range></report_metadata>
  <policy_published><domain>d</domain></policy_published>
  <record><row><source_ip>1.2.3.4</source_ip><count>1</count><policy_evaluated/></row></record>
</feedback>`
	r, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Rows[0].Disposition != "none" || r.Rows[0].DKIM != "fail" || r.Rows[0].SPF != "fail" {
		t.Errorf("defaults wrong: %+v", r.Rows[0])
	}
}
