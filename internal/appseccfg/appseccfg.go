// Package appseccfg is the single source of the
// crowdsecurity/jabali-appsec AppSec config (ADR-0083 shape;
// ADR-0102 follow-up). It replaces two hand-written copies — the
// bash heredoc in install.sh and the Go string template in
// panel-agent security_crowdsec.go (which fully regenerates the file
// on every geoblock Apply). Both now call Render so the schema, the
// ADR-0102 admin-API allowlist, and the geoblock pre_eval cannot
// drift.
//
// The inband-rule set is host-dependent (install.sh presence-gates it
// by stat'ing /etc/crowdsec/appsec-rules/*; the agent uses its fixed
// set) so it is a parameter, not baked in — only the template is
// single-sourced.
package appseccfg

import "strings"

// Opts is everything a caller must supply. Inband is the presence-gated
// rule list (caller decides which files exist). Mode is
// "off"|"allow"|"deny"; Countries are ISO-3166-1 alpha-2 codes used by
// the geoblock pre_eval for allow/deny modes.
type Opts struct {
	Mode           string
	Countries      []string
	Inband         []string
	AdminAllowlist bool
}

// allowExpr/denyExpr reproduce panel-agent renderAppSecGeoblockRule
// EXACTLY (byte parity is the point of single-sourcing):
//
//	no countries  → allow `""`         deny ``
//	N countries    → allow `"A", "B", ""`  deny `"A", "B"`
func geoExpr(codes []string, allow bool) string {
	if len(codes) == 0 {
		if allow {
			return `""`
		}
		return ""
	}
	q := make([]string, len(codes))
	for i, c := range codes {
		q[i] = `"` + c + `"`
	}
	j := strings.Join(q, ", ")
	if allow {
		return j + `, ""`
	}
	return j
}

// Render returns the full jabali-appsec.yaml body. Deterministic:
// header(+inband) → on_match (ADR-0102) → pre_eval (geoblock).
func Render(o Opts) string {
	mode := o.Mode
	if mode == "" {
		mode = "off"
	}
	csv := strings.Join(o.Countries, ",")

	var b strings.Builder
	b.WriteString("# Managed by jabali — M27 AppSec config.\n")
	b.WriteString("# DO NOT hand-edit. Set via the admin Security → CrowdSec tab OR\n")
	b.WriteString("# POST /api/v1/admin/security/crowdsec/appsec/geoblock.\n")
	b.WriteString("# jabali-mode: " + mode + "\n")
	b.WriteString("# jabali-countries: " + csv + "\n")
	b.WriteString("name: crowdsecurity/jabali-appsec\n")
	b.WriteString("default_remediation: ban\n")
	b.WriteString("inband_rules:\n")
	for _, r := range o.Inband {
		b.WriteString(" - " + r + "\n")
	}

	// ADR-0102 (amended 2026-05-19): the ENTIRE panel API (/api/v1/)
	// is Kratos-session-gated, same-origin SPA control plane — not
	// public web attack surface. The CRS generic ruleset false-
	// positives on legitimate REST: rule 911100 "Method not allowed
	// by policy" blocks every PATCH/PUT/DELETE (DNS records, etc.),
	// and body-inspection flags JSON/ULID payloads. Narrowing to
	// /api/v1/admin/ left the SPA's own mutations (e.g.
	// PATCH /api/v1/dns/records/:id) WAF-blocked with an opaque 403.
	// Exempt the whole prefix; public vhosts keep full AppSec.
	if o.AdminAllowlist {
		b.WriteString(`on_match:
 - filter: req.URL.Path startsWith "/api/v1/"
   apply:
    - CancelEvent()
    - CancelAlert()
    - SetRemediation("allow")
`)
	}

	// Geoblock pre_eval (ADR-0060). off = inert (no block).
	switch mode {
	case "allow":
		// allow-list: drop everything NOT in the set; the trailing ""
		// keeps requests whose GeoIP lookup yields no country (local /
		// private ranges) reachable.
		b.WriteString(`pre_eval:
 - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode not in [` + geoExpr(o.Countries, true) + `]
   apply:
    - DropRequest("Forbidden Country (jabali allow-list)")
`)
	case "deny":
		b.WriteString(`pre_eval:
 - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode in [` + geoExpr(o.Countries, false) + `]
   apply:
    - DropRequest("Forbidden Country (jabali deny-list)")
`)
	}
	return b.String()
}
