package appseccfg

import (
	"strings"
	"testing"
)

// Single source of the crowdsecurity/jabali-appsec config (ADR-0083
// shape; ADR-0102 follow-up). Today the template is hand-written
// twice — install.sh (bash) + panel-agent security_crowdsec.go (Go,
// full-regenerate on geoblock Apply) — guaranteed to drift
// (feedback_cross_boundary_contracts). Render() is the one producer;
// both callers pass their host-dependent inband set.

func mustContain(t *testing.T, s, sub, why string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%s: missing %q\n--- got ---\n%s", why, sub, s)
	}
}

func TestRender_HeaderAndInband(t *testing.T) {
	out := Render(Opts{
		Mode:           "off",
		Inband:         []string{"crowdsecurity/base-config", "crowdsecurity/vpatch-*", "crowdsecurity/generic-*"},
		AdminAllowlist: true,
	})
	mustContain(t, out, "# Managed by jabali", "header")
	mustContain(t, out, "# jabali-mode: off", "mode header line")
	mustContain(t, out, "name: crowdsecurity/jabali-appsec", "config name")
	mustContain(t, out, "default_remediation: ban", "default remediation")
	mustContain(t, out, "inband_rules:\n - crowdsecurity/base-config\n - crowdsecurity/vpatch-*\n - crowdsecurity/generic-*\n", "inband list in order")
	// ADR-0102: admin API allowlist present.
	mustContain(t, out, `on_match:
 - filter: req.URL.Path startsWith "/api/v1/"
   apply:
    - CancelEvent()
    - CancelAlert()
    - SetRemediation("allow")
`, "ADR-0102 panel-API allowlist (amended: whole /api/v1/)")
	if strings.Contains(out, "pre_eval:") {
		t.Fatalf("mode=off must NOT emit pre_eval:\n%s", out)
	}
	// Deterministic order: inband_rules < on_match.
	if strings.Index(out, "inband_rules:") > strings.Index(out, "on_match:") {
		t.Fatal("inband_rules must precede on_match")
	}
}

func TestRender_GeoblockModes(t *testing.T) {
	inb := []string{"crowdsecurity/vpatch-*", "crowdsecurity/generic-*"}

	allow := Render(Opts{Mode: "allow", Countries: []string{"IL", "US"}, Inband: inb, AdminAllowlist: true})
	mustContain(t, allow, "# jabali-mode: allow", "allow mode header")
	mustContain(t, allow, "# jabali-countries: IL,US", "countries header")
	mustContain(t, allow, `pre_eval:
 - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode not in ["IL", "US", ""]
   apply:
    - DropRequest("Forbidden Country (jabali allow-list)")
`, "allow pre_eval (note trailing empty for allow)")

	deny := Render(Opts{Mode: "deny", Countries: []string{"RU"}, Inband: inb, AdminAllowlist: true})
	mustContain(t, deny, `pre_eval:
 - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode in ["RU"]
   apply:
    - DropRequest("Forbidden Country (jabali deny-list)")
`, "deny pre_eval")
	// on_match must coexist with pre_eval, and precede it.
	mustContain(t, deny, "on_match:", "on_match present in deny mode")
	if strings.Index(deny, "on_match:") > strings.Index(deny, "pre_eval:") {
		t.Fatal("on_match must precede pre_eval")
	}
}

func TestRender_AllowlistOptOut(t *testing.T) {
	out := Render(Opts{Mode: "off", Inband: []string{"crowdsecurity/vpatch-*"}, AdminAllowlist: false})
	if strings.Contains(out, "on_match:") {
		t.Fatal("AdminAllowlist=false must NOT emit on_match")
	}
}

func TestRender_GeoExprAgentParity(t *testing.T) {
	// Matches panel-agent renderAppSecGeoblockRule exactly.
	a0 := Render(Opts{Mode: "allow", Countries: nil, Inband: []string{"x"}})
	mustContain(t, a0, `not in [""]`, "allow + no countries → not in [\"\"]")
	d0 := Render(Opts{Mode: "deny", Countries: nil, Inband: []string{"x"}})
	mustContain(t, d0, `in []`, "deny + no countries → in []")
	a2 := Render(Opts{Mode: "allow", Countries: []string{"IL", "US"}, Inband: []string{"x"}})
	mustContain(t, a2, `not in ["IL", "US", ""]`, "allow + N → trailing empty")
	d2 := Render(Opts{Mode: "deny", Countries: []string{"RU"}, Inband: []string{"x"}})
	mustContain(t, d2, `in ["RU"]`, "deny + N")
}
