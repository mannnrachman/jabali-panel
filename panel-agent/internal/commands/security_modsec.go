package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// M26 Step 2 (ADR-0055). ModSecurity global config + audit-log surface
// for the admin Security tab. Per-domain modsec_enabled is handled
// separately by the per-vhost reconciler (M26 Step 5) — this file only
// touches the GLOBAL engine state in /etc/nginx/modsecurity.conf and
// the OWASP CRS paranoia level.

// modsecConfPath is the Debian-shipped libnginx-mod-http-modsecurity
// stock config (NOT the upstream-tarball /etc/modsecurity/modsecurity.conf).
// The Step 1 install_modsecurity flips this to `SecRuleEngine Off`.
const modsecConfPath = "/etc/nginx/modsecurity.conf"

// crsSetupPath holds the paranoia level. CRS exposes it via
// `tx.paranoia_level` / `tx.executing_paranoia_level` set early in
// crs-setup.conf via SecAction.
const crsSetupPath = "/etc/modsecurity/crs/crs-setup.conf"

// modsecAuditLogPath is the JSON-formatted audit log written by
// libmodsecurity3 when SecAuditLog is enabled in modsecurity.conf.
const modsecAuditLogPath = "/var/log/modsec_audit.log"

func modsecInvalidArg(msg string) error {
	return &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: msg}
}

func modsecInternal(msg string, err error) error {
	return &agentwire.AgentError{
		Code:    agentwire.CodeInternal,
		Message: fmt.Sprintf("%s: %v", msg, err),
	}
}

// ---- security.modsec.global.get --------------------------------------------

type modsecGlobalGetResponse struct {
	EngineMode string `json:"engine_mode"`
	Paranoia   int    `json:"paranoia"`
}

var (
	secRuleEngineRe = regexp.MustCompile(`(?m)^\s*SecRuleEngine\s+(On|Off|DetectionOnly)\b`)
	paranoiaSetRe   = regexp.MustCompile(`(?m)^\s*SecAction\s+"id:900000,phase:1,nolog,pass,t:none,setvar:tx\.(?:executing_)?paranoia_level=(\d)"`)
)

func modsecGlobalGetHandler(_ context.Context, _ json.RawMessage) (any, error) {
	resp := modsecGlobalGetResponse{EngineMode: "Off", Paranoia: 1}
	if data, err := os.ReadFile(modsecConfPath); err == nil {
		if m := secRuleEngineRe.FindStringSubmatch(string(data)); m != nil {
			resp.EngineMode = m[1]
		}
	}
	if data, err := os.ReadFile(crsSetupPath); err == nil {
		if m := paranoiaSetRe.FindStringSubmatch(string(data)); m != nil {
			resp.Paranoia, _ = strconv.Atoi(m[1])
		}
	}
	return resp, nil
}

// ---- security.modsec.global.set --------------------------------------------

type modsecGlobalSetParams struct {
	EngineMode string `json:"engine_mode"`
	Paranoia   int    `json:"paranoia"`
}

type modsecGlobalSetResponse struct {
	Applied        bool `json:"applied"`
	NginxReloaded  bool `json:"nginx_reloaded"`
}

var validEngineModes = map[string]bool{"On": true, "Off": true, "DetectionOnly": true}

func modsecGlobalSetHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p modsecGlobalSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, modsecInvalidArg(fmt.Sprintf("parse params: %v", err))
	}
	if !validEngineModes[p.EngineMode] {
		return nil, modsecInvalidArg("engine_mode must be On|Off|DetectionOnly")
	}
	if p.Paranoia < 1 || p.Paranoia > 4 {
		return nil, modsecInvalidArg("paranoia must be 1..4")
	}

	// Update SecRuleEngine in-place.
	if data, err := os.ReadFile(modsecConfPath); err == nil {
		updated := secRuleEngineRe.ReplaceAllString(string(data), "SecRuleEngine "+p.EngineMode)
		if err := modsecAtomicWrite(modsecConfPath, []byte(updated), 0o644); err != nil {
			return nil, modsecInternal("write modsec conf", err)
		}
	} else {
		return nil, modsecInternal("read modsec conf", err)
	}

	// Update paranoia level in crs-setup.conf. CRS ships the SecAction
	// commented out by default — uncomment + set if missing.
	if data, err := os.ReadFile(crsSetupPath); err == nil {
		s := string(data)
		if paranoiaSetRe.MatchString(s) {
			s = paranoiaSetRe.ReplaceAllString(s,
				fmt.Sprintf(`SecAction "id:900000,phase:1,nolog,pass,t:none,setvar:tx.executing_paranoia_level=%d"`, p.Paranoia))
		} else {
			s += fmt.Sprintf("\n# Managed by jabali — M26 Step 4\nSecAction \"id:900000,phase:1,nolog,pass,t:none,setvar:tx.executing_paranoia_level=%d\"\n", p.Paranoia)
		}
		if err := modsecAtomicWrite(crsSetupPath, []byte(s), 0o644); err != nil {
			return nil, modsecInternal("write crs-setup", err)
		}
	}

	// nginx -t before reload — fail loud rather than break tenant traffic.
	if out, err := exec.CommandContext(ctx, "nginx", "-t").CombinedOutput(); err != nil {
		return nil, modsecInternal("nginx -t failed: "+string(out), err)
	}
	resp := modsecGlobalSetResponse{Applied: true}
	if err := exec.CommandContext(ctx, "systemctl", "reload", "nginx").Run(); err != nil {
		return resp, modsecInternal("nginx reload", err)
	}
	resp.NginxReloaded = true
	return resp, nil
}

func modsecAtomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- security.modsec.audit.tail --------------------------------------------

type modsecAuditTailParams struct {
	Lines int `json:"lines,omitempty"`
}

type modsecAuditEntry struct {
	TS       string   `json:"ts,omitempty"`
	Client   string   `json:"client,omitempty"`
	URI      string   `json:"uri,omitempty"`
	RuleIDs  []string `json:"rule_ids,omitempty"`
	Severity string   `json:"severity,omitempty"`
	Raw      string   `json:"raw,omitempty"`
	ParseErr bool     `json:"parse_error,omitempty"`
}

type modsecAuditTailResponse struct {
	Entries []modsecAuditEntry `json:"entries"`
}

func modsecAuditTailHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p modsecAuditTailParams
	_ = json.Unmarshal(params, &p)
	if p.Lines == 0 {
		p.Lines = 50
	}
	if p.Lines < 1 || p.Lines > 1000 {
		return nil, modsecInvalidArg("lines must be 1..1000")
	}

	out, err := exec.CommandContext(ctx, "tail", "-n", strconv.Itoa(p.Lines), modsecAuditLogPath).Output()
	if err != nil {
		// File may legitimately not exist on a host where modsec is
		// installed but never inspected anything. Return empty list.
		if _, statErr := os.Stat(modsecAuditLogPath); os.IsNotExist(statErr) {
			return modsecAuditTailResponse{Entries: []modsecAuditEntry{}}, nil
		}
		return nil, modsecInternal("tail audit log", err)
	}

	resp := modsecAuditTailResponse{Entries: []modsecAuditEntry{}}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry := parseModsecAuditLine(line)
		resp.Entries = append(resp.Entries, entry)
	}
	return resp, nil
}

// parseModsecAuditLine handles libmodsecurity3's JSON-formatted line.
// Falls back to a raw-text entry with parse_error=true on unparseable
// lines so the operator never sees binary garbage in the panel UI.
func parseModsecAuditLine(line string) modsecAuditEntry {
	var raw struct {
		Transaction struct {
			TimeStamp string `json:"time_stamp"`
			ClientIP  string `json:"client_ip"`
			Request   struct {
				URI string `json:"uri"`
			} `json:"request"`
			Messages []struct {
				Details struct {
					RuleID   string `json:"ruleId"`
					Severity string `json:"severity"`
				} `json:"details"`
			} `json:"messages"`
		} `json:"transaction"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return modsecAuditEntry{Raw: line, ParseErr: true}
	}
	entry := modsecAuditEntry{
		TS:     raw.Transaction.TimeStamp,
		Client: raw.Transaction.ClientIP,
		URI:    raw.Transaction.Request.URI,
	}
	for _, m := range raw.Transaction.Messages {
		if m.Details.RuleID != "" {
			entry.RuleIDs = append(entry.RuleIDs, m.Details.RuleID)
		}
		// Take the highest severity seen on this transaction.
		if m.Details.Severity != "" && (entry.Severity == "" || m.Details.Severity < entry.Severity) {
			entry.Severity = m.Details.Severity
		}
	}
	return entry
}

func init() {
	Default.Register("security.modsec.global.get", modsecGlobalGetHandler)
	Default.Register("security.modsec.global.set", modsecGlobalSetHandler)
	Default.Register("security.modsec.audit.tail", modsecAuditTailHandler)
}
