package commands

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// renderVhostModsec executes vhostTemplate with explicit ModSec flags
// and returns the rendered config. Defaults shape an SSL-enabled vhost
// so the template falls into the modsec block.
func renderVhostModsec(t *testing.T, modsecEnabled, modsecGlobalEnabled bool) string {
	t.Helper()
	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	vd := vhostData{
		Domain:              "example.test",
		DocRoot:             "/home/alice/public_html/example.test",
		IsEnabled:           true,
		IndexDirective:      "index index.html;",
		SSLCertPath:         "/etc/letsencrypt/live/example.test/fullchain.pem",
		SSLKeyPath:          "/etc/letsencrypt/live/example.test/privkey.pem",
		ModSecEnabled:       modsecEnabled,
		ModSecGlobalEnabled: modsecGlobalEnabled,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vd); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	return buf.String()
}

func TestVhostTemplate_Modsec_BothFlagsTrue_EmitsBlock(t *testing.T) {
	out := renderVhostModsec(t, true, true)
	if !strings.Contains(out, "modsecurity on;") {
		t.Errorf("expected `modsecurity on;` directive when both flags true.\nGot:\n%s", out)
	}
	if !strings.Contains(out, "/etc/nginx/modsec/main.conf") {
		t.Errorf("expected modsecurity_rules_file pointing at /etc/nginx/modsec/main.conf.\nGot:\n%s", out)
	}
}

func TestVhostTemplate_Modsec_PerDomainOff_NoBlock(t *testing.T) {
	out := renderVhostModsec(t, false, true)
	if strings.Contains(out, "modsecurity") {
		t.Errorf("per-domain off must omit modsecurity directives.\nGot:\n%s", out)
	}
}

func TestVhostTemplate_Modsec_GlobalOff_NoBlock(t *testing.T) {
	// Global master switch wins — even if per-domain says on, global off
	// → no directives. ADR-0055 + plan Step 5 task 3 invariant.
	out := renderVhostModsec(t, true, false)
	if strings.Contains(out, "modsecurity") {
		t.Errorf("global modsec off must omit directives even when domain flag is on.\nGot:\n%s", out)
	}
}

func TestVhostTemplate_Modsec_BothOff_NoBlock(t *testing.T) {
	out := renderVhostModsec(t, false, false)
	if strings.Contains(out, "modsecurity") {
		t.Errorf("both flags off must omit modsecurity directives.\nGot:\n%s", out)
	}
}
