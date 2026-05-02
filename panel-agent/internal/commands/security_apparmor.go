// security_apparmor — M40 (ADR-0086) thin wrapper around aa-status
// + aa-{enforce,complain} for the admin Security tab. Two commands:
//
//   security.apparmor.status    — list profiles + recent denials
//   security.apparmor.set_mode  — flip a single jabali-* profile
//                                 between complain and enforce
//
// We hard-code an allowlist of profile labels we own; arbitrary
// profile-name input from the panel is refused. Operator who needs
// to flip a non-allowlisted profile uses SSH.

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"
	"strings"
	"time"
)

const apparmorCallTimeout = 10 * time.Second

// allowedProfiles enumerates the jabali-shipped profiles the panel
// can toggle. Adding a new profile here MUST be paired with adding
// the profile file under install/apparmor/.
var allowedProfiles = map[string]bool{
	"jabali-panel":   true,
	"jabali-agent":   true,
	"jabali-bulwark": true,
	"jabali-kratos":  true,
	"stalwart-mail":  true,
}

// apparmorProfileFile maps a profile name to its on-disk file path.
// aa-enforce/aa-complain need a path that resolves either via PATH or
// directly to a profile file; profile names like "jabali-bulwark"
// don't resolve via PATH on Debian, so we always pass the explicit
// file path.
func apparmorProfileFile(name string) string {
	switch name {
	case "jabali-panel":
		return "/etc/apparmor.d/usr.local.bin.jabali-panel-api"
	case "jabali-agent":
		return "/etc/apparmor.d/usr.local.bin.jabali-agent"
	case "jabali-bulwark":
		return "/etc/apparmor.d/usr.local.bin.jabali-bulwark"
	case "jabali-kratos":
		return "/etc/apparmor.d/usr.local.bin.jabali-kratos"
	case "stalwart-mail":
		return "/etc/apparmor.d/usr.local.bin.stalwart-mail"
	}
	return ""
}

type apparmorProfile struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

type apparmorStatusResponse struct {
	Enabled  bool              `json:"enabled"`
	Profiles []apparmorProfile `json:"profiles"`
	// Reason: human-readable when Enabled=false (e.g. "kernel LSM
	// missing", "GRUB pending reboot").
	Reason string `json:"reason,omitempty"`
}

func mwApparmorStatusHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, apparmorCallTimeout)
	defer cancel()

	resp := apparmorStatusResponse{Profiles: []apparmorProfile{}}

	out, err := osexec.CommandContext(ctx, "aa-status", "--json").Output()
	if err != nil {
		// aa-status returns non-zero on disabled / not-installed —
		// surface as Enabled=false, not as an internal error.
		resp.Enabled = false
		resp.Reason = fmt.Sprintf("aa-status: %v", err)
		return resp, nil
	}

	resp.Enabled = true

	// aa-status --json shape (Debian 13 / apparmor 3.x):
	// { "version": "...", "profiles": { "<name>": "enforce|complain", ... } }
	var raw struct {
		Profiles map[string]string `json:"profiles"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return resp, nil
	}

	for name, mode := range raw.Profiles {
		// Filter to jabali profiles + the system-service profiles we ship.
		if !strings.HasPrefix(name, "jabali-") &&
			name != "stalwart-mail" {
			continue
		}
		// Skip complain-mode child shadow profiles (e.g. "jabali-panel//null-/usr/sbin/...").
		if strings.Contains(name, "//") {
			continue
		}
		resp.Profiles = append(resp.Profiles, apparmorProfile{
			Name: name,
			Mode: mode,
		})
	}
	return resp, nil
}

type apparmorSetModeRequest struct {
	Profile string `json:"profile"`
	Mode    string `json:"mode"` // complain|enforce
}

func mwApparmorSetModeHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var req apparmorSetModeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, mwInvalidArg("malformed JSON body")
	}
	if !allowedProfiles[req.Profile] {
		return nil, mwInvalidArg("profile not in allowlist")
	}
	var tool string
	switch req.Mode {
	case "enforce":
		tool = "aa-enforce"
	case "complain":
		tool = "aa-complain"
	default:
		return nil, mwInvalidArg("mode must be enforce|complain")
	}

	ctx, cancel := context.WithTimeout(ctx, apparmorCallTimeout)
	defer cancel()

	// aa-{enforce,complain} accepts either the profile-file path OR
	// the profile label. We pass the label — works on Debian 13 AA 3.x.
	profilePath := apparmorProfileFile(req.Profile)
	if profilePath == "" {
		return nil, mwInvalidArg("profile has no file path mapping")
	}
	out, err := osexec.CommandContext(ctx, tool, profilePath).CombinedOutput()
	if err != nil {
		return nil, mwInternal(fmt.Sprintf("%s %s: %s", tool, req.Profile, string(out)), err)
	}
	return map[string]any{
		"profile": req.Profile,
		"mode":    req.Mode,
	}, nil
}

func init() {
	Default.Register("security.apparmor.status", mwApparmorStatusHandler)
	Default.Register("security.apparmor.set_mode", mwApparmorSetModeHandler)
}
