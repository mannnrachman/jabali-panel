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
	if p.Type != "" && !validHubTypes[p.Type] {
		return nil, csInvalidArg("type must be collections|parsers|scenarios|postoverflows|appsec-rules|appsec-configs")
	}
	// `cscli hub list -a` returns ALL hub items (installed + available);
	// without -a only installed items come back. We need the full set so
	// the UI can show "available to install" entries.
	out, err := runCscliJSON(ctx, "hub", "list", "-a")
	if err != nil {
		return nil, csInternal("cscli hub list -a", err)
	}
	// cscli omits `installed` / `enabled` boolean fields. Installed state
	// is derived from `local_path` being non-empty (item exists on disk),
	// and enabled state from the `status` string containing "enabled".
	resp := csHubListResponse{Items: []csHubItem{}}
	var raw map[string][]struct {
		Name      string `json:"name"`
		LocalPath string `json:"local_path"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(out, &raw); err == nil {
		for typ, items := range raw {
			if p.Type != "" && typ != p.Type {
				continue
			}
			for _, it := range items {
				installed := it.LocalPath != ""
				enabled := installed && strings.Contains(it.Status, "enabled") && !strings.Contains(it.Status, "disabled")
				resp.Items = append(resp.Items, csHubItem{
					Name: it.Name, Type: typ, Installed: installed, Enabled: enabled,
				})
			}
		}
	}
	return resp, nil
}

// ---- security.crowdsec.hub.{install,remove} -------------------------------
//
// Wraps `cscli <type> install|remove <name>` for curated free hub items.
// Reloads crowdsec on success so newly installed parsers/scenarios take
// effect without operator intervention. Type is whitelisted against
// validHubTypes to prevent shell smuggling via the cscli verb.

type csHubMutateParams struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Force allows reinstall when already present (cscli --force).
	Force bool `json:"force,omitempty"`
}

// hubItemNameRE matches `crowdsecurity/sshd`, `crowdsecurity/appsec-virtual-patching`,
// `myorg/my-collection.v2`. Rejects shell metacharacters by construction.
var hubItemNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func csHubInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csHubMutateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg("invalid params")
	}
	if !validHubTypes[p.Type] {
		return nil, csInvalidArg("type must be collections|parsers|scenarios|postoverflows|appsec-rules|appsec-configs")
	}
	if !hubItemNameRE.MatchString(p.Name) {
		return nil, csInvalidArg("name must match <author>/<item>")
	}
	args := []string{p.Type, "install", p.Name}
	if p.Force {
		args = append(args, "--force")
	}
	cmd := exec.CommandContext(ctx, "cscli", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, csInternal(fmt.Sprintf("cscli %s install: %s", p.Type, strings.TrimSpace(string(out))), err)
	}
	_ = exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run()
	return map[string]any{"type": p.Type, "name": p.Name, "installed": true}, nil
}

func csHubRemoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csHubMutateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg("invalid params")
	}
	if !validHubTypes[p.Type] {
		return nil, csInvalidArg("type must be collections|parsers|scenarios|postoverflows|appsec-rules|appsec-configs")
	}
	if !hubItemNameRE.MatchString(p.Name) {
		return nil, csInvalidArg("name must match <author>/<item>")
	}
	cmd := exec.CommandContext(ctx, "cscli", p.Type, "remove", p.Name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, csInternal(fmt.Sprintf("cscli %s remove: %s", p.Type, strings.TrimSpace(string(out))), err)
	}
	_ = exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run()
	return map[string]any{"type": p.Type, "name": p.Name, "installed": false}, nil
}

// ---- security.crowdsec.appsec.geoblock.{get,set} --------------------------

// appsecRulePath is where our single server-wide geoblock rule lives.
// Written by appsecGeoblockSetHandler, read by appsecGeoblockGetHandler.
// install.sh `install_crowdsec_appsec` seeds the file at mode=off so
// crowdsec can parse the appsec-config on first boot.
//
// M27 fix: pre_eval hooks live in appsec-CONFIG, not appsec-rules.
// Earlier path /etc/crowdsec/appsec-rules/jabali-geoblock.yaml was
// loaded as a rule but the pre_eval hook never fired (rules use a
// different schema with `rules:` block, not `pre_eval:`).
const appsecRulePath = "/etc/crowdsec/appsec-configs/jabali-appsec.yaml"

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
	if err := os.MkdirAll("/etc/crowdsec/appsec-configs", 0o755); err != nil {
		return nil, csInternal("mkdir appsec-configs", err)
	}
	// Best-effort cleanup of pre-M27-fix path. Safe to leave if absent.
	_ = os.Remove("/etc/crowdsec/appsec-rules/jabali-geoblock.yaml")
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
	// Build expr list per mode. Allow-mode appends "" so private IPs
	// (RFC 1918, loopback — GeoIPEnrich returns "") still pass.
	// Deny-mode does NOT append "" — we never want to deny unresolvable.
	allowExpr := `""`
	denyExpr := ""
	if len(countries) > 0 {
		quoted := make([]string, len(countries))
		for i, c := range countries {
			quoted[i] = `"` + c + `"`
		}
		allowExpr = strings.Join(quoted, ", ") + `, ""`
		denyExpr = strings.Join(quoted, ", ")
	}

	// Common header + base appsec-config skeleton. inband_rules block
	// loads the upstream vpatch-* CVE rules + base-config. pre_eval is
	// appended below per-mode (off = no pre_eval).
	header := `# Managed by jabali — M27 AppSec config.
# DO NOT hand-edit. Set via the admin Security → CrowdSec tab OR
# POST /api/v1/admin/security/crowdsec/appsec/geoblock.
# jabali-mode: ` + mode + `
# jabali-countries: ` + csv + `
name: crowdsecurity/jabali-appsec
default_remediation: ban
inband_rules:
 - crowdsecurity/base-config
 - crowdsecurity/vpatch-*
`

	switch mode {
	case "off":
		// No pre_eval block — vpatch rules still evaluate, geoblock inert.
		return header
	case "allow":
		return header + `pre_eval:
 - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode not in [` + allowExpr + `]
   apply:
    - DropRequest("Forbidden Country (jabali allow-list)")
`
	case "deny":
		return header + `pre_eval:
 - filter: IsInBand == true && GeoIPEnrich(req.RemoteAddr)?.Country.IsoCode in [` + denyExpr + `]
   apply:
    - DropRequest("Forbidden Country (jabali deny-list)")
`
	}
	return header
}

// ---- M27 Step 2: security.crowdsec.allowlists.{list,add,remove} -----------

// jabaliAllowlistName is the single server-wide allowlist jabali manages
// (ADR-0061). Pinned so repeated add/remove calls are idempotent.
const jabaliAllowlistName = "jabali-admin-allowlist"

type csAllowlistEntryWire struct {
	Value     string `json:"value"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// cscli allowlists inspect payload (v1.7.x). Top-level includes name + items.
type rawAllowlistInspect struct {
	Items []struct {
		Value       string `json:"value"`
		Description string `json:"description"`
		CreatedAt   string `json:"created_at"`
	} `json:"items"`
}

func csAllowlistsListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := runCscliJSON(ctx, "allowlists", "inspect", jabaliAllowlistName)
	if err != nil {
		// cscli exits nonzero when the allowlist doesn't exist. Surface
		// as empty list — first list before first add is a normal state.
		return map[string]any{"items": []csAllowlistEntryWire{}}, nil
	}
	var raw rawAllowlistInspect
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, csInternal("parse cscli allowlists inspect", err)
	}
	items := make([]csAllowlistEntryWire, 0, len(raw.Items))
	for _, it := range raw.Items {
		items = append(items, csAllowlistEntryWire{
			Value:     it.Value,
			Reason:    it.Description,
			CreatedAt: it.CreatedAt,
		})
	}
	return map[string]any{"items": items}, nil
}

type csAllowlistAddParams struct {
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// ensureJabaliAllowlist creates the named allowlist if missing. Idempotent:
// if cscli reports "already exists" (nonzero exit + distinctive stderr)
// we treat it as success. We don't rely on the exit code alone because
// cscli versions differ.
func ensureJabaliAllowlist(ctx context.Context) error {
	check := exec.CommandContext(ctx, "cscli", "allowlists", "inspect", jabaliAllowlistName)
	if err := check.Run(); err == nil {
		return nil
	}
	create := exec.CommandContext(ctx, "cscli", "allowlists", "create",
		jabaliAllowlistName, "-d", "jabali-managed admin allowlist")
	out, err := create.CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "already exists") {
			return nil
		}
		return fmt.Errorf("cscli allowlists create: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func csAllowlistsAddHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csAllowlistAddParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	value := strings.TrimSpace(p.Value)
	if value == "" {
		return nil, csInvalidArg("value is required")
	}
	// Validate IP or CIDR. Reject everything else (country, ASN — those
	// are decision scopes, not allowlist-value shapes).
	if net.ParseIP(value) == nil {
		if _, _, err := net.ParseCIDR(value); err != nil {
			return nil, csInvalidArg("value must be an IP or CIDR")
		}
	}
	if l := len(p.Reason); l < 3 || l > 200 {
		return nil, csInvalidArg("reason length must be 3..200")
	}
	if err := ensureJabaliAllowlist(ctx); err != nil {
		return nil, csInternal("ensure allowlist", err)
	}
	cmd := exec.CommandContext(ctx, "cscli", "allowlists", "add",
		jabaliAllowlistName, value, "-d", p.Reason)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, csInternal(fmt.Sprintf("cscli allowlists add: %s", strings.TrimSpace(string(out))), err)
	}
	return map[string]any{"value": value}, nil
}

type csAllowlistRemoveParams struct {
	Value string `json:"value"`
}

func csAllowlistsRemoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csAllowlistRemoveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	value := strings.TrimSpace(p.Value)
	if value == "" {
		return nil, csInvalidArg("value is required")
	}
	cmd := exec.CommandContext(ctx, "cscli", "allowlists", "remove",
		jabaliAllowlistName, value)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no such") {
			return nil, &agentwire.AgentError{Code: agentwire.CodeNotFound, Message: "allowlist entry not found"}
		}
		return nil, csInternal(fmt.Sprintf("cscli allowlists remove: %s", strings.TrimSpace(string(out))), err)
	}
	return map[string]any{}, nil
}

// ---- M27 Step 3: security.crowdsec.alerts.{list,inspect} -----------------

type csAlertWire struct {
	ID             int    `json:"id"`
	Scenario       string `json:"scenario"`
	SourceIP       string `json:"source_ip"`
	SourceScope    string `json:"source_scope"`
	SourceValue    string `json:"source_value"`
	EventsCount    int    `json:"events_count"`
	DecisionsCount int    `json:"decisions_count"`
	StartedAt      string `json:"started_at"`
	StoppedAt      string `json:"stopped_at"`
	MachineID      string `json:"machine_id"`
}

type rawAlertEntry struct {
	ID          int    `json:"id"`
	Scenario    string `json:"scenario"`
	MachineID   string `json:"machine_id"`
	StartAt     string `json:"start_at"`
	StopAt      string `json:"stop_at"`
	EventsCount int    `json:"events_count"`
	Source      struct {
		IP    string `json:"ip"`
		Scope string `json:"scope"`
		Value string `json:"value"`
	} `json:"source"`
	Decisions []json.RawMessage `json:"decisions"`
}

func csAlertsListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	// Cap to 100 alerts within the last 24h. Server-chosen to avoid
	// OOMing the panel on a busy host; operator can still `cscli alerts
	// list --all` from the host if they need more.
	out, err := runCscliJSON(ctx, "alerts", "list", "--since", "24h", "--limit", "100")
	if err != nil {
		return nil, csInternal("cscli alerts list", err)
	}
	var raw []rawAlertEntry
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, csInternal("parse cscli alerts list", err)
	}
	items := make([]csAlertWire, 0, len(raw))
	for _, a := range raw {
		items = append(items, csAlertWire{
			ID:             a.ID,
			Scenario:       a.Scenario,
			SourceIP:       a.Source.IP,
			SourceScope:    a.Source.Scope,
			SourceValue:    a.Source.Value,
			EventsCount:    a.EventsCount,
			DecisionsCount: len(a.Decisions),
			StartedAt:      a.StartAt,
			StoppedAt:      a.StopAt,
			MachineID:      a.MachineID,
		})
	}
	return map[string]any{"items": items}, nil
}

type csAlertsInspectParams struct {
	ID int `json:"id"`
}

func csAlertsInspectHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csAlertsInspectParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	if p.ID <= 0 {
		return nil, csInvalidArg("id must be a positive integer")
	}
	out, err := runCscliJSON(ctx, "alerts", "inspect", strconv.Itoa(p.ID), "--details")
	if err != nil {
		return nil, csInternal("cscli alerts inspect", err)
	}
	// Passthrough — the detail shape is rich (meta + events + decisions)
	// and the UI renders it via Descriptions + nested Table.
	var raw json.RawMessage = out
	return map[string]any{"alert": raw}, nil
}

// ---- M27 Step 6: security.crowdsec.scenarios.list + profiles.{get,set} ----

const (
	profilesPath       = "/etc/crowdsec/profiles.yaml"
	profilesBeginMark  = "# jabali-begin-overrides"
	profilesEndMark    = "# jabali-end-overrides"
	profilesWarningCmt = "# DO NOT HAND-EDIT — rewritten by jabali on Save. Edits inside these markers are lost.\n# To add manual profiles, place them AFTER the jabali-end-overrides line below."
)

type rawScenarioEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type csScenarioWire struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func csScenariosListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := runCscliJSON(ctx, "scenarios", "list")
	if err != nil {
		return nil, csInternal("cscli scenarios list", err)
	}
	var wrap struct {
		Scenarios []rawScenarioEntry `json:"scenarios"`
	}
	if err := json.Unmarshal(out, &wrap); err != nil {
		return nil, csInternal("parse scenarios", err)
	}
	items := make([]csScenarioWire, 0, len(wrap.Scenarios))
	for _, s := range wrap.Scenarios {
		items = append(items, csScenarioWire{Name: s.Name, Description: s.Description})
	}
	return map[string]any{"items": items}, nil
}

type csProfileOverride struct {
	Scenario string `json:"scenario"`
	Action   string `json:"action"` // captcha | off
}

// scanOverridesFromProfiles parses the jabali marker-bounded block and
// returns the overrides previously persisted. Everything outside the
// markers is ignored.
func scanOverridesFromProfiles() ([]csProfileOverride, error) {
	raw, err := os.ReadFile(profilesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []csProfileOverride{}, nil
		}
		return nil, err
	}
	text := string(raw)
	start := strings.Index(text, profilesBeginMark)
	end := strings.Index(text, profilesEndMark)
	if start < 0 || end < 0 || end < start {
		return []csProfileOverride{}, nil
	}
	block := text[start:end]
	// Minimal parse: find scenario filter + decision type per doc.
	// Format produced by renderOverrides is deterministic so we can
	// scan for `GetScenario() == "<name>"` + `type: <action>`.
	scenRE := regexp.MustCompile(`GetScenario\(\) == "([^"]+)"`)
	typeRE := regexp.MustCompile(`type:\s*(captcha|bypass)`)
	scens := scenRE.FindAllStringSubmatch(block, -1)
	types := typeRE.FindAllStringSubmatch(block, -1)
	out := make([]csProfileOverride, 0, len(scens))
	for i, s := range scens {
		if i >= len(types) {
			break
		}
		action := "off"
		if types[i][1] == "captcha" {
			action = "captcha"
		}
		out = append(out, csProfileOverride{Scenario: s[1], Action: action})
	}
	return out, nil
}

func csProfilesGetHandler(_ context.Context, _ json.RawMessage) (any, error) {
	overrides, err := scanOverridesFromProfiles()
	if err != nil {
		return nil, csInternal("read profiles", err)
	}
	return map[string]any{"overrides": overrides}, nil
}

// renderOverridesBlock produces the jabali-managed marker block. Each
// override becomes one profile doc. "off" uses decision type=bypass
// (CrowdSec's built-in "skip this alert" remediation).
func renderOverridesBlock(overrides []csProfileOverride) string {
	if len(overrides) == 0 {
		return fmt.Sprintf("%s\n%s\n%s\n", profilesBeginMark, profilesWarningCmt, profilesEndMark)
	}
	var b strings.Builder
	b.WriteString(profilesBeginMark)
	b.WriteString("\n")
	b.WriteString(profilesWarningCmt)
	b.WriteString("\n---\n")
	for i, o := range overrides {
		action := "captcha"
		if o.Action == "off" {
			action = "bypass"
		}
		// Sanitize scenario name for the profile `name:` field — alnum
		// + dashes + slashes stay; everything else → '-'.
		safe := regexp.MustCompile(`[^A-Za-z0-9_/.-]`).ReplaceAllString(o.Scenario, "-")
		b.WriteString(fmt.Sprintf("name: jabali-override-%s\n", safe))
		b.WriteString(fmt.Sprintf("filters:\n - Alert.Remediation == true && Alert.GetScenario() == %q\n", o.Scenario))
		b.WriteString(fmt.Sprintf("decisions:\n - type: %s\n   duration: 4h\n", action))
		b.WriteString("on_success: break\n")
		if i < len(overrides)-1 {
			b.WriteString("---\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(profilesEndMark)
	b.WriteString("\n")
	return b.String()
}

type csProfilesSetParams struct {
	Overrides []csProfileOverride `json:"overrides"`
}

func csProfilesSetHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csProfilesSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	for _, o := range p.Overrides {
		if o.Action != "captcha" && o.Action != "off" {
			return nil, csInvalidArg(`action must be "captcha" or "off"`)
		}
		if o.Scenario == "" {
			return nil, csInvalidArg("scenario name required")
		}
	}
	raw, err := os.ReadFile(profilesPath)
	if err != nil {
		return nil, csInternal("read profiles.yaml", err)
	}
	text := string(raw)
	newBlock := renderOverridesBlock(p.Overrides)

	// Strip any existing jabali block (marker-bounded).
	startIdx := strings.Index(text, profilesBeginMark)
	endIdx := strings.Index(text, profilesEndMark)
	if startIdx >= 0 && endIdx >= 0 && endIdx > startIdx {
		// Include trailing newline after endMark if present.
		endOfLine := strings.Index(text[endIdx:], "\n")
		if endOfLine >= 0 {
			endIdx = endIdx + endOfLine + 1
		} else {
			endIdx = endIdx + len(profilesEndMark)
		}
		text = text[:startIdx] + text[endIdx:]
	}
	// Insert new block at the very top so jabali profiles evaluate first.
	text = newBlock + "---\n" + strings.TrimLeft(text, "\n")

	bak := profilesPath + ".bak"
	_ = os.WriteFile(bak, raw, 0o644)
	if err := os.WriteFile(profilesPath, []byte(text), 0o644); err != nil {
		return nil, csInternal("write profiles.yaml", err)
	}

	// Pre-flight: `crowdsec -t` (exists in v1.7.x per probe).
	if out, terr := exec.CommandContext(ctx, "crowdsec", "-t").CombinedOutput(); terr != nil {
		// Restore backup + surface error.
		_ = os.WriteFile(profilesPath, raw, 0o644)
		return nil, csInternal(fmt.Sprintf("crowdsec -t: %s", strings.TrimSpace(string(out))), terr)
	}
	if err := exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run(); err != nil {
		_ = os.WriteFile(profilesPath, raw, 0o644)
		_ = exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run()
		return nil, csInternal("systemctl reload crowdsec after write", err)
	}
	return map[string]any{"overrides": p.Overrides}, nil
}

// ---- M27 Step 5: security.crowdsec.captcha.apply -------------------------

const bouncerConfPath = "/etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf"

type csCaptchaApplyParams struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`
	SiteKey   string `json:"site_key"`
	SecretKey string `json:"secret_key"`
}

var validCaptchaProviders = map[string]bool{
	"":          true, // empty when disabled
	"hcaptcha":  true,
	"recaptcha": true,
	"turnstile": true,
}

// rewriteBouncerConfKeys updates specific KEY=value lines in the bouncer
// conf WITHOUT rewriting the whole file — the file was authored by
// install_crowdsec_nginx_bouncer (M26) with AppSec settings that must
// survive. Each key in `updates` replaces the existing line; missing
// keys are appended.
func rewriteBouncerConfKeys(path string, updates map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	seen := make(map[string]bool, len(updates))
	for i, line := range lines {
		for key, val := range updates {
			prefix := key + "="
			if strings.HasPrefix(line, prefix) {
				lines[i] = prefix + val
				seen[key] = true
			}
		}
	}
	for key, val := range updates {
		if !seen[key] {
			lines = append(lines, key+"="+val)
		}
	}
	out := strings.Join(lines, "\n")
	tmp, err := os.CreateTemp("", "bouncer-conf-*.tmp")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func csCaptchaApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csCaptchaApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	if !validCaptchaProviders[p.Provider] {
		return nil, csInvalidArg("provider must be hcaptcha|recaptcha|turnstile")
	}
	// When disabling, clear provider + set fallback back to ban.
	updates := map[string]string{}
	if p.Enabled {
		if p.Provider == "" {
			return nil, csInvalidArg("provider required when enabled")
		}
		if len(p.SiteKey) == 0 || len(p.SiteKey) > 512 {
			return nil, csInvalidArg("site_key length must be 1..512")
		}
		if len(p.SecretKey) == 0 || len(p.SecretKey) > 512 {
			return nil, csInvalidArg("secret_key required when enabling")
		}
		updates["CAPTCHA_PROVIDER"] = p.Provider
		updates["SITE_KEY"] = p.SiteKey
		updates["SECRET_KEY"] = p.SecretKey
		updates["FALLBACK_REMEDIATION"] = "captcha"
	} else {
		updates["CAPTCHA_PROVIDER"] = ""
		updates["SITE_KEY"] = ""
		updates["SECRET_KEY"] = ""
		updates["FALLBACK_REMEDIATION"] = "ban"
	}
	// Back up to .bak; restore on nginx-t failure.
	bakPath := bouncerConfPath + ".bak"
	if existing, err := os.ReadFile(bouncerConfPath); err == nil {
		_ = os.WriteFile(bakPath, existing, 0o600)
	}
	if err := rewriteBouncerConfKeys(bouncerConfPath, updates); err != nil {
		return nil, csInternal("rewrite bouncer conf", err)
	}
	if err := exec.CommandContext(ctx, "nginx", "-t").Run(); err != nil {
		// Restore from bak + surface the error to the operator.
		if bak, rerr := os.ReadFile(bakPath); rerr == nil {
			_ = os.WriteFile(bouncerConfPath, bak, 0o600)
		}
		return nil, csInternal("nginx -t failed after bouncer conf rewrite", err)
	}
	if err := exec.CommandContext(ctx, "systemctl", "reload", "nginx").Run(); err != nil {
		return nil, csInternal("systemctl reload nginx", err)
	}
	return map[string]any{"enabled": p.Enabled}, nil
}

// ---- M27 Step 4: security.crowdsec.console.enroll ------------------------

var consoleKeyRE = regexp.MustCompile(`^[A-Za-z0-9-]{16,128}$`)

type csConsoleEnrollParams struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

func csConsoleEnrollHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csConsoleEnrollParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	key := strings.TrimSpace(p.Key)
	if !consoleKeyRE.MatchString(key) {
		return nil, csInvalidArg("key must be 16-128 alnum + dash chars")
	}
	args := []string{"console", "enroll"}
	if n := strings.TrimSpace(p.Name); n != "" {
		if len(n) > 64 {
			return nil, csInvalidArg("name length must be <= 64")
		}
		args = append(args, "--name", n)
	}
	args = append(args, key)
	cmd := exec.CommandContext(ctx, "cscli", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, csInternal(fmt.Sprintf("cscli console enroll: %s", strings.TrimSpace(string(out))), err)
	}
	// crowdsec restart picks up the new enrollment. Soft-fail if the
	// service isn't under our init (dev containers).
	_ = exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run()
	return map[string]any{"pending": true}, nil
}

// csConsoleStatusHandler wraps `cscli console status -o json`.
// Returns a list of {name, enabled, description} option entries.
// Valid options (v1.7.x): custom, manual, tainted, context, console_management.
type csConsoleOption struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
}

// consoleOptionDescriptions mirrors the description column from the
// human-formatted `cscli console status` table. cscli's JSON output
// returns a flat {option: bool} map with no descriptions; we carry
// the labels here so the UI can render them without a second call.
var consoleOptionDescriptions = map[string]string{
	"custom":             "Forward alerts from custom scenarios to the console",
	"manual":             "Forward manual decisions to the console",
	"tainted":            "Forward alerts from tainted scenarios to the console",
	"context":            "Forward context with alerts to the console",
	"console_management": "Receive decisions from console",
}

func csConsoleStatusHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	out, err := runCscliJSON(ctx, "console", "status")
	if err != nil {
		return nil, csInternal("cscli console status", err)
	}
	// v1.7.x ships {option: bool} — flat map. Descriptions only appear
	// in the human-formatted table, so we emit them from our own
	// constant lookup.
	var raw map[string]bool
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, csInternal("parse console status", err)
	}
	names := []string{"custom", "manual", "tainted", "context", "console_management"}
	items := make([]csConsoleOption, 0, len(names))
	for _, n := range names {
		enabled, ok := raw[n]
		if !ok {
			continue
		}
		items = append(items, csConsoleOption{
			Name:        n,
			Enabled:     enabled,
			Description: consoleOptionDescriptions[n],
		})
	}
	return map[string]any{"items": items}, nil
}

var validConsoleOptions = map[string]bool{
	"custom": true, "manual": true, "tainted": true, "context": true,
	"console_management": true, "all": true,
}

type csConsoleOptionParams struct {
	Option string `json:"option"`
}

func csConsoleEnableHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csConsoleOptionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	if !validConsoleOptions[p.Option] {
		return nil, csInvalidArg("option must be custom|manual|tainted|context|console_management|all")
	}
	cmd := exec.CommandContext(ctx, "cscli", "console", "enable", p.Option)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, csInternal(fmt.Sprintf("cscli console enable: %s", strings.TrimSpace(string(out))), err)
	}
	_ = exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run()
	return map[string]any{"option": p.Option, "enabled": true}, nil
}

func csConsoleDisableHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p csConsoleOptionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, csInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	if !validConsoleOptions[p.Option] {
		return nil, csInvalidArg("option must be custom|manual|tainted|context|console_management|all")
	}
	cmd := exec.CommandContext(ctx, "cscli", "console", "disable", p.Option)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, csInternal(fmt.Sprintf("cscli console disable: %s", strings.TrimSpace(string(out))), err)
	}
	_ = exec.CommandContext(ctx, "systemctl", "reload", "crowdsec").Run()
	return map[string]any{"option": p.Option, "enabled": false}, nil
}

func init() {
	Default.Register("security.crowdsec.status", csStatusHandler)
	Default.Register("security.crowdsec.decisions.list", csDecisionsListHandler)
	Default.Register("security.crowdsec.decisions.add", csDecisionsAddHandler)
	Default.Register("security.crowdsec.decisions.delete", csDecisionsDeleteHandler)
	Default.Register("security.crowdsec.bouncers.list", csBouncersListHandler)
	Default.Register("security.crowdsec.metrics", csMetricsHandler)
	Default.Register("security.crowdsec.hub.list", csHubListHandler)
	Default.Register("security.crowdsec.hub.install", csHubInstallHandler)
	Default.Register("security.crowdsec.hub.remove", csHubRemoveHandler)
	Default.Register("security.crowdsec.appsec.geoblock.get", csAppSecGeoblockGetHandler)
	Default.Register("security.crowdsec.appsec.geoblock.set", csAppSecGeoblockSetHandler)
	Default.Register("security.crowdsec.allowlists.list", csAllowlistsListHandler)
	Default.Register("security.crowdsec.allowlists.add", csAllowlistsAddHandler)
	Default.Register("security.crowdsec.allowlists.remove", csAllowlistsRemoveHandler)
	Default.Register("security.crowdsec.alerts.list", csAlertsListHandler)
	Default.Register("security.crowdsec.alerts.inspect", csAlertsInspectHandler)
	Default.Register("security.crowdsec.console.enroll", csConsoleEnrollHandler)
	Default.Register("security.crowdsec.console.status", csConsoleStatusHandler)
	Default.Register("security.crowdsec.console.enable", csConsoleEnableHandler)
	Default.Register("security.crowdsec.console.disable", csConsoleDisableHandler)
	Default.Register("security.crowdsec.captcha.apply", csCaptchaApplyHandler)
	Default.Register("security.crowdsec.scenarios.list", csScenariosListHandler)
	Default.Register("security.crowdsec.profiles.get", csProfilesGetHandler)
	Default.Register("security.crowdsec.profiles.set", csProfilesSetHandler)
}
