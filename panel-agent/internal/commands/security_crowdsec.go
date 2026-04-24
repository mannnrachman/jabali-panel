package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// M26 Step 2 (ADR-0053). CrowdSec LAPI surface for the admin Security
// tab. Every handler shells `cscli`; cscli reads
// /etc/crowdsec/local_api_credentials.yaml which the M26 install_crowdsec
// pinned to the unix socket /run/crowdsec/api.sock (ADR-0050).

// ---- shared helpers --------------------------------------------------------

func csInvalidArg(msg string) error {
	return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: msg}
}

func csInternal(msg string, err error) error {
	return &agentwire.AgentError{
		Code:    agentwire.CodeInternal,
		Message: fmt.Sprintf("%s: %v", msg, err),
	}
}

// runCscliJSON runs `cscli <args...> -o json` and returns the raw JSON.
// Error is wrapped as CodeInternal with the captured stderr.
func runCscliJSON(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{}, args...)
	full = append(full, "-o", "json")
	cmd := exec.CommandContext(ctx, "cscli", full...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- security.crowdsec.status ----------------------------------------------

type csStatusResponse struct {
	Running        bool   `json:"running"`
	LapiReachable  bool   `json:"lapi_reachable"`
	Version        string `json:"version,omitempty"`
}

func csStatusHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	resp := csStatusResponse{}
	if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "crowdsec").Run(); err == nil {
		resp.Running = true
	}
	// `cscli lapi status` prints success line on stdout when reachable.
	if out, err := exec.CommandContext(ctx, "cscli", "lapi", "status").CombinedOutput(); err == nil {
		resp.LapiReachable = strings.Contains(string(out), "successfully interact with Local API")
	}
	if out, err := exec.CommandContext(ctx, "cscli", "version").Output(); err == nil {
		// First non-empty line typically holds "version: vX.Y.Z" or similar.
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "version:") {
				resp.Version = strings.TrimSpace(strings.TrimPrefix(line, "version:"))
				resp.Version = strings.TrimPrefix(resp.Version, "Version:")
				break
			}
		}
	}
	return resp, nil
}

// ---- security.crowdsec.decisions.list --------------------------------------

type csDecisionsListParams struct {
	Scope string `json:"scope,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type csDecision struct {
	ID       int    `json:"id"`
	IP       string `json:"ip"`
	Duration string `json:"duration"`
	Reason   string `json:"reason"`
	Scenario string `json:"scenario"`
	Until    string `json:"until"`
}

type csDecisionsListResponse struct {
	Decisions []csDecision `json:"decisions"`
}

// rawDecisionEntry mirrors the "ip" group cscli returns under
// `cscli decisions list -o json` — one outer entry per source with
// nested decisions[]. Field names match upstream JSON.
type rawDecisionEntry struct {
	Decisions []struct {
		ID       int    `json:"id"`
		Value    string `json:"value"`
		Duration string `json:"duration"`
		Scenario string `json:"scenario"`
		Origin   string `json:"origin"`
		Until    string `json:"until"`
		Type     string `json:"type"`
	} `json:"decisions"`
}

func csDecisionsListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csDecisionsListParams
	_ = json.Unmarshal(params, &p)
	args := []string{"decisions", "list"}
	if p.Scope != "" {
		if p.Scope != "ip" && p.Scope != "range" && p.Scope != "country" && p.Scope != "as" {
			return nil, csInvalidArg("scope must be ip|range|country|as")
		}
		args = append(args, "--scope", p.Scope)
	}
	if p.Limit > 0 {
		if p.Limit > 1000 {
			return nil, csInvalidArg("limit max 1000")
		}
		args = append(args, "--limit", strconv.Itoa(p.Limit))
	}
	out, err := runCscliJSON(ctx, args...)
	if err != nil {
		return nil, csInternal("cscli decisions list", err)
	}
	resp := csDecisionsListResponse{Decisions: []csDecision{}}
	// cscli returns either a [] or `null` when there are no decisions.
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" {
		return resp, nil
	}
	var raw []rawDecisionEntry
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, csInternal("parse decisions json", err)
	}
	for _, entry := range raw {
		for _, d := range entry.Decisions {
			resp.Decisions = append(resp.Decisions, csDecision{
				ID:       d.ID,
				IP:       d.Value,
				Duration: d.Duration,
				Reason:   d.Origin + "/" + d.Scenario,
				Scenario: d.Scenario,
				Until:    d.Until,
			})
		}
	}
	return resp, nil
}

// ---- security.crowdsec.decisions.add ---------------------------------------

// csDecisionsAddParams covers all four CrowdSec decision scopes.
//
//	scope=ip      value=203.0.113.7           single address
//	scope=range   value=203.0.113.0/24        CIDR block
//	scope=country value=IL                    ISO 3166-1 alpha-2 code
//	scope=as      value=AS64500 or value=64500 ASN, optional AS/as prefix
//
// Country bans rely on the GeoIP2 enricher (crowdsecurity/geoip-enrich)
// which crowdsec installs by default on fresh hosts. If an operator
// disabled it the decision still materialises but the bouncer has no
// way to resolve country → IP, so the ban is inert. The UI leaves that
// operational concern to the CrowdSec hub tab.
type csDecisionsAddParams struct {
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Duration string `json:"duration"`
	Reason   string `json:"reason"`
}

type csDecisionsAddResponse struct {
	ID int `json:"id"`
}

var csCountryRE = regexp.MustCompile(`^[A-Z]{2}$`)
var csASNRE = regexp.MustCompile(`^\d+$`)

// cscliScope maps our lowercase scope names to cscli's canonical
// capitalisation. cscli accepts lowercase in most commands but the
// `--scope` flag is case-sensitive for Country + AS in some 1.7.x
// builds — keep the mapping explicit so we don't debug a regression
// across upstream versions.
var cscliScope = map[string]string{
	"ip":      "Ip",
	"range":   "Range",
	"country": "Country",
	"as":      "AS",
}

// normalizeASN strips a leading "AS"/"as" and returns the numeric
// portion for validation + value passing.
func normalizeASN(v string) string {
	upper := strings.ToUpper(v)
	return strings.TrimPrefix(upper, "AS")
}

func csDecisionsAddHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csDecisionsAddParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	canonical, ok := cscliScope[p.Scope]
	if !ok {
		return nil, csInvalidArg("scope must be ip|range|country|as")
	}
	value := strings.TrimSpace(p.Value)
	switch p.Scope {
	case "ip":
		if ip := net.ParseIP(value); ip == nil {
			return nil, csInvalidArg("value must be a valid IP for scope=ip")
		}
	case "range":
		if _, _, err := net.ParseCIDR(value); err != nil {
			return nil, csInvalidArg("value must be a valid CIDR for scope=range")
		}
	case "country":
		value = strings.ToUpper(value)
		if !csCountryRE.MatchString(value) {
			return nil, csInvalidArg("value must be a 2-letter ISO 3166-1 country code for scope=country")
		}
	case "as":
		value = normalizeASN(value)
		if !csASNRE.MatchString(value) {
			return nil, csInvalidArg("value must be an ASN number (e.g. 64500 or AS64500) for scope=as")
		}
	}
	if _, err := time.ParseDuration(p.Duration); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("duration: %v", err))
	}
	if l := len(p.Reason); l < 3 || l > 200 {
		return nil, csInvalidArg("reason length must be 3..200")
	}
	cmd := exec.CommandContext(ctx, "cscli", "decisions", "add",
		"--scope", canonical, "--value", value,
		"--duration", p.Duration, "--reason", p.Reason)
	if _, err := cmd.CombinedOutput(); err != nil {
		return nil, csInternal("cscli decisions add", err)
	}
	// cscli decisions add doesn't return the new ID — look up by (scope,value).
	id := csLookupDecisionID(ctx, canonical, value)
	return csDecisionsAddResponse{ID: id}, nil
}

func csLookupDecisionID(ctx context.Context, canonicalScope, value string) int {
	out, err := runCscliJSON(ctx, "decisions", "list", "--scope", canonicalScope, "--value", value)
	if err != nil {
		return 0
	}
	var raw []rawDecisionEntry
	if err := json.Unmarshal(out, &raw); err != nil {
		return 0
	}
	for _, entry := range raw {
		for _, d := range entry.Decisions {
			if d.Value == value {
				return d.ID
			}
		}
	}
	return 0
}

// ---- security.crowdsec.decisions.delete ------------------------------------

type csDecisionsDeleteParams struct {
	ID int    `json:"id,omitempty"`
	IP string `json:"ip,omitempty"`
}

func csDecisionsDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csDecisionsDeleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	args := []string{"decisions", "delete"}
	switch {
	case p.ID > 0:
		args = append(args, "--id", strconv.Itoa(p.ID))
	case p.IP != "":
		if _, _, err := net.ParseCIDR(p.IP); err != nil {
			if ip := net.ParseIP(p.IP); ip == nil {
				return nil, csInvalidArg("ip must be IP or CIDR")
			}
		}
		args = append(args, "--ip", p.IP)
	default:
		return nil, csInvalidArg("either id or ip required")
	}
	if _, err := exec.CommandContext(ctx, "cscli", args...).CombinedOutput(); err != nil {
		return nil, csInternal("cscli decisions delete", err)
	}
	return map[string]bool{"deleted": true}, nil
}

// ---- security.crowdsec.bouncers.list ---------------------------------------

type csBouncer struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Revoked  bool   `json:"revoked"`
	LastPull string `json:"last_pull"`
}

type csBouncersListResponse struct {
	Bouncers []csBouncer `json:"bouncers"`
}

func csBouncersListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := runCscliJSON(ctx, "bouncers", "list")
	if err != nil {
		return nil, csInternal("cscli bouncers list", err)
	}
	resp := csBouncersListResponse{Bouncers: []csBouncer{}}
	var raw []struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Revoked  bool   `json:"revoked"`
		LastPull string `json:"last_pull"`
	}
	if err := json.Unmarshal(out, &raw); err == nil {
		for _, r := range raw {
			resp.Bouncers = append(resp.Bouncers, csBouncer(r))
		}
	}
	return resp, nil
}

// ---- security.crowdsec.metrics ---------------------------------------------

type csMetricsResponse struct {
	Parsed         int `json:"parsed"`
	Unparsed       int `json:"unparsed"`
	Buckets        int `json:"buckets"`
	DecisionsActive int `json:"decisions_active"`
	AlertsTotal    int `json:"alerts_total"`
}

func csMetricsHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	// `cscli metrics -o json` returns a deeply nested object. We extract
	// summary counters that map cleanly to the dashboard.
	out, err := runCscliJSON(ctx, "metrics")
	if err != nil {
		return nil, csInternal("cscli metrics", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(out, &raw)
	resp := csMetricsResponse{}
	resp.Parsed = sumInt(raw, "parsers", "")
	resp.Unparsed = sumInt(raw, "unparsed", "")
	resp.Buckets = sumInt(raw, "buckets", "")
	// active decisions + total alerts come from cscli decisions list / alerts list
	if d, err := runCscliJSON(ctx, "decisions", "list"); err == nil {
		var entries []rawDecisionEntry
		_ = json.Unmarshal(d, &entries)
		for _, e := range entries {
			resp.DecisionsActive += len(e.Decisions)
		}
	}
	if a, err := runCscliJSON(ctx, "alerts", "list"); err == nil {
		var alerts []any
		_ = json.Unmarshal(a, &alerts)
		resp.AlertsTotal = len(alerts)
	}
	return resp, nil
}

// sumInt walks a {section: {key: {counter: N, ...}}} structure and sums all numeric
// leaf values under the named section. Handles cscli's verbose metrics output
// without coupling us to the full schema.
func sumInt(raw map[string]any, section, _ string) int {
	v, ok := raw[section].(map[string]any)
	if !ok {
		return 0
	}
	total := 0
	for _, sub := range v {
		switch s := sub.(type) {
		case map[string]any:
			for _, leaf := range s {
				if f, ok := leaf.(float64); ok {
					total += int(f)
				}
			}
		case float64:
			total += int(s)
		}
	}
	return total
}

// ---- security.crowdsec.hub.list --------------------------------------------

type csHubListParams struct {
	Type string `json:"type,omitempty"`
}

type csHubItem struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
}

type csHubListResponse struct {
	Items []csHubItem `json:"items"`
}

var validHubTypes = map[string]bool{
	"collections": true, "parsers": true, "scenarios": true, "postoverflows": true, "appsec-rules": true, "appsec-configs": true,
}

func csHubListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csHubListParams
	_ = json.Unmarshal(params, &p)
	args := []string{"hub", "list"}
	if p.Type != "" {
		if !validHubTypes[p.Type] {
			return nil, csInvalidArg("type must be collections|parsers|scenarios|postoverflows|appsec-rules|appsec-configs")
		}
		// `cscli hub list -t <type>` filters
	}
	out, err := runCscliJSON(ctx, args...)
	if err != nil {
		return nil, csInternal("cscli hub list", err)
	}
	resp := csHubListResponse{Items: []csHubItem{}}
	var raw map[string][]struct {
		Name      string `json:"name"`
		Installed bool   `json:"installed"`
		Enabled   bool   `json:"enabled"`
	}
	if err := json.Unmarshal(out, &raw); err == nil {
		for typ, items := range raw {
			if p.Type != "" && typ != p.Type {
				continue
			}
			for _, it := range items {
				resp.Items = append(resp.Items, csHubItem{
					Name: it.Name, Type: typ, Installed: it.Installed, Enabled: it.Enabled,
				})
			}
		}
	}
	return resp, nil
}

// ---- security.crowdsec.appsec.geoblock.{get,set} --------------------------

// appsecRulePath is where our single server-wide geoblock rule lives.
// Written by appsecGeoblockSetHandler, read by appsecGeoblockGetHandler.
// install.sh `install_crowdsec_appsec` seeds the file at mode=off so
// crowdsec can parse the collection on first boot.
const appsecRulePath = "/etc/crowdsec/appsec-rules/jabali-geoblock.yaml"

type csAppSecGeoblockGetResponse struct {
	Mode      string   `json:"mode"`
	Countries []string `json:"countries"`
}

type csAppSecGeoblockSetParams struct {
	Mode      string   `json:"mode"`
	Countries []string `json:"countries"`
}

// csAppSecGeoblockModes is the closed enum the UI + panel-api + agent
// all share. Keep in sync with the ADR.
var csAppSecGeoblockModes = map[string]struct{}{
	"off":   {},
	"allow": {},
	"deny":  {},
}

// csCountryCodeRE pins 2-letter ISO 3166-1 alpha-2. CrowdSec's
// GeoIPEnrich returns ISO codes in this shape; anything else fails the
// `not in [...]` filter silently.
var csCountryCodeRE = regexp.MustCompile(`^[A-Z]{2}$`)

func csAppSecGeoblockGetHandler(_ context.Context, _ json.RawMessage) (any, error) {
	resp := csAppSecGeoblockGetResponse{Mode: "off", Countries: []string{}}
	body, err := os.ReadFile(appsecRulePath)
	if err != nil {
		// Missing rule file → mode off. install.sh seeds it, so this
		// only fires on systems that predate M26 AppSec or that the
		// operator has hand-deleted.
		if os.IsNotExist(err) {
			return resp, nil
		}
		return nil, csInternal("read appsec rule", err)
	}
	// The rule file carries `# jabali-mode: <mode>` and
	// `# jabali-countries: <csv>` as comment markers so we don't
	// roundtrip through a full YAML parser for the readback.
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "# jabali-mode:"):
			resp.Mode = strings.TrimSpace(strings.TrimPrefix(line, "# jabali-mode:"))
		case strings.HasPrefix(line, "# jabali-countries:"):
			csv := strings.TrimSpace(strings.TrimPrefix(line, "# jabali-countries:"))
			if csv != "" {
				resp.Countries = strings.Split(csv, ",")
			}
		}
	}
	if _, ok := csAppSecGeoblockModes[resp.Mode]; !ok {
		resp.Mode = "off"
	}
	return resp, nil
}

func csAppSecGeoblockSetHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csAppSecGeoblockSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	if _, ok := csAppSecGeoblockModes[p.Mode]; !ok {
		return nil, csInvalidArg("mode must be off|allow|deny")
	}
	// Uppercase + dedupe + validate country codes.
	seen := map[string]struct{}{}
	cleaned := make([]string, 0, len(p.Countries))
	for _, c := range p.Countries {
		code := strings.ToUpper(strings.TrimSpace(c))
		if code == "" {
			continue
		}
		if !csCountryCodeRE.MatchString(code) {
			return nil, csInvalidArg(fmt.Sprintf("country %q must be a 2-letter ISO 3166-1 code", c))
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		cleaned = append(cleaned, code)
	}
	if (p.Mode == "allow" || p.Mode == "deny") && len(cleaned) == 0 {
		return nil, csInvalidArg("mode " + p.Mode + " requires at least one country")
	}
	body := renderAppSecGeoblockRule(p.Mode, cleaned)
	if err := os.MkdirAll("/etc/crowdsec/appsec-rules", 0o755); err != nil {
		return nil, csInternal("mkdir appsec-rules", err)
	}
	tmp := appsecRulePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return nil, csInternal("write appsec rule tmp", err)
	}
	if err := os.Rename(tmp, appsecRulePath); err != nil {
		return nil, csInternal("rename appsec rule", err)
	}
	// Reload — SIGHUP re-reads rules without dropping the LAPI socket.
	// `systemctl reload crowdsec` sends SIGHUP when ExecReload is set
	// (crowdsec.service ships that). If reload is not wired (older
	// packaging) fall back to restart.
	if out, err := exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").CombinedOutput(); err != nil {
		if out2, err2 := exec.CommandContext(ctx, "systemctl", "restart", "crowdsec").CombinedOutput(); err2 != nil {
			return nil, csInternal("systemctl reload/restart crowdsec",
				fmt.Errorf("reload: %v %s; restart: %v %s", err, out, err2, out2))
		}
	}
	return csAppSecGeoblockGetResponse{Mode: p.Mode, Countries: cleaned}, nil
}

// renderAppSecGeoblockRule emits the YAML rule body. The filter uses
// CrowdSec's expr syntax with GeoIPEnrich — see
// https://doc.crowdsec.net/docs/next/appsec/rules_examples/#5-geoblocking.
// The two `# jabali-...` markers at the top let the get handler parse
// (mode, countries) back out without a YAML round-trip.
func renderAppSecGeoblockRule(mode string, countries []string) string {
	// Empty-string tolerance in the list is intentional: GeoIPEnrich
	// returns "" for private IPs (RFC 1918, loopback). Without "" in
	// the list, operators who turn on "allow [FR]" would immediately
	// lock out their own health checks from localhost.
	csv := strings.Join(countries, ",")
	// Build the expr list — quote each code; always append "".
	exprList := `""`
	if len(countries) > 0 {
		quoted := make([]string, len(countries))
		for i, c := range countries {
			quoted[i] = `"` + c + `"`
		}
		exprList = strings.Join(quoted, ", ") + `, ""`
	}

	header := `# Managed by jabali — M26 AppSec geoblock rule.
# DO NOT hand-edit. Set via the admin Security → CrowdSec tab OR
# POST /api/v1/admin/security/crowdsec/appsec/geoblock.
# jabali-mode: ` + mode + `
# jabali-countries: ` + csv + `
`

	switch mode {
	case "off":
		// A valid rule file with no filter hooks — crowdsec parses
		// it clean and does nothing. Keeps the file present (and
		// parseable) so the get handler can still read the markers.
		return header + `name: jabali/appsec-geoblock
description: Server-wide AppSec geoblock (currently OFF).
`
	case "allow":
		return header + `name: jabali/appsec-geoblock
description: Server-wide AppSec geoblock — allow-list mode.
pre_eval:
  - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode not in [` + exprList + `]
    apply:
      - DropRequest("Forbidden Country (jabali allow-list)")
`
	case "deny":
		return header + `name: jabali/appsec-geoblock
description: Server-wide AppSec geoblock — deny-list mode.
pre_eval:
  - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode in [` + exprList + `]
    apply:
      - DropRequest("Forbidden Country (jabali deny-list)")
`
	}
	return header
}

func init() {
	Default.Register("security.crowdsec.status", csStatusHandler)
	Default.Register("security.crowdsec.decisions.list", csDecisionsListHandler)
	Default.Register("security.crowdsec.decisions.add", csDecisionsAddHandler)
	Default.Register("security.crowdsec.decisions.delete", csDecisionsDeleteHandler)
	Default.Register("security.crowdsec.bouncers.list", csBouncersListHandler)
	Default.Register("security.crowdsec.metrics", csMetricsHandler)
	Default.Register("security.crowdsec.hub.list", csHubListHandler)
	Default.Register("security.crowdsec.appsec.geoblock.get", csAppSecGeoblockGetHandler)
	Default.Register("security.crowdsec.appsec.geoblock.set", csAppSecGeoblockSetHandler)
}
