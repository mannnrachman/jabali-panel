// Package commands — security_snuffleupagus.go (M41, ADR-0088)
//
// Agent-side handlers for the Snuffleupagus PHP-hardening module.
// snuffleupagus.status — reports loaded .so per PHP minor + active rule SHA.
// snuffleupagus.reload — graceful-reload every per-PHP-version FPM unit
//                        after the panel reconciler rewrites active.rules.
//
// The journalctl-tail incident ingest pipeline lives in a sibling
// long-running goroutine (snuffleupagus_ingest.go); it pushes rows back
// to panel-api via the internal /admin/security/snuffleupagus/_ingest
// endpoint so panel-api owns persistence.
package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"sort"
)

const snuffleupagusActiveRulesPath = "/etc/jabali/snuffleupagus/active.rules"

var phpMinorDirRe = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

type snuffleupagusStatusResponse struct {
	Enabled           bool                          `json:"enabled"`
	Mode              string                        `json:"mode"`
	ActiveRulesSha256 string                        `json:"active_rules_sha256,omitempty"`
	PhpVersionsLoaded []snuffleupagusPhpVersionStat `json:"php_versions_loaded"`
}

type snuffleupagusPhpVersionStat struct {
	Minor       string `json:"minor"`
	ExtensionSO string `json:"extension_so"`
	Loaded      bool   `json:"loaded"`
}

func snuffleupagusStatusHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	resp := snuffleupagusStatusResponse{
		PhpVersionsLoaded: []snuffleupagusPhpVersionStat{},
	}

	// Active rules SHA for change-detection vs DB.
	if data, err := os.ReadFile(snuffleupagusActiveRulesPath); err == nil {
		sum := sha256.Sum256(data)
		resp.ActiveRulesSha256 = hex.EncodeToString(sum[:])
		// Heuristic mode read: file starts with "sp.global.enable(0);"
		// in off mode; otherwise parse `mode=` header line.
		switch {
		case bytes.Contains(data, []byte("sp.global.enable(0);")):
			resp.Mode = "off"
		case bytes.Contains(data, []byte(".simulation();")):
			resp.Mode = "simulation"
		default:
			resp.Mode = "enforce"
		}
		resp.Enabled = resp.Mode != "off"
	}

	// Discover installed PHP minors via /etc/php/<minor>/cli/conf.d.
	entries, err := os.ReadDir("/etc/php")
	if err == nil {
		var minors []string
		for _, e := range entries {
			if e.IsDir() && phpMinorDirRe.MatchString(e.Name()) {
				minors = append(minors, e.Name())
			}
		}
		sort.Strings(minors)
		for _, m := range minors {
			so := filepath.Join("/usr/lib/php/jabali-snuffleupagus", m, "snuffleupagus.so")
			loaded := false
			if _, err := os.Stat(so); err == nil {
				// Confirm the conf.d wiring resolves — checks the cli
				// drop-in symlink so we don't lie about FPM-side.
				cliDrop := filepath.Join("/etc/php", m, "cli/conf.d/30-jabali-snuffleupagus.ini")
				if _, err := os.Stat(cliDrop); err == nil {
					loaded = true
				}
			}
			resp.PhpVersionsLoaded = append(resp.PhpVersionsLoaded, snuffleupagusPhpVersionStat{
				Minor:       m,
				ExtensionSO: so,
				Loaded:      loaded,
			})
		}
	}

	return resp, nil
}

type snuffleupagusReloadResponse struct {
	Pools []snuffleupagusPoolReloadResult `json:"pools"`
}

type snuffleupagusPoolReloadResult struct {
	Unit   string `json:"unit"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// snuffleupagusReloadHandler issues `systemctl reload-or-restart phpX.Y-fpm.service`
// for every PHP minor that has the Snuffleupagus extension built. Reload
// is graceful: in-flight requests finish before workers respawn with the
// new active.rules.
func snuffleupagusReloadHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	resp := snuffleupagusReloadResponse{Pools: []snuffleupagusPoolReloadResult{}}

	entries, err := os.ReadDir("/etc/php")
	if err != nil {
		// No PHP installed — not an error from this handler's point of
		// view; the panel logs it and moves on.
		return resp, nil
	}

	var minors []string
	for _, e := range entries {
		if !e.IsDir() || !phpMinorDirRe.MatchString(e.Name()) {
			continue
		}
		// Skip minors without the .so — they have nothing to reload.
		so := filepath.Join("/usr/lib/php/jabali-snuffleupagus", e.Name(), "snuffleupagus.so")
		if _, err := os.Stat(so); err != nil {
			continue
		}
		minors = append(minors, e.Name())
	}
	sort.Strings(minors)

	for _, m := range minors {
		unit := fmt.Sprintf("php%s-fpm.service", m)
		cmd := osexec.CommandContext(ctx, "systemctl", "reload-or-restart", unit)
		out, err := cmd.CombinedOutput()
		if err != nil {
			resp.Pools = append(resp.Pools, snuffleupagusPoolReloadResult{
				Unit:   unit,
				OK:     false,
				Detail: fmt.Sprintf("%v: %s", err, string(out)),
			})
			continue
		}
		resp.Pools = append(resp.Pools, snuffleupagusPoolReloadResult{Unit: unit, OK: true})
	}

	return resp, nil
}

func init() {
	Default.Register("snuffleupagus.status", snuffleupagusStatusHandler)
	Default.Register("snuffleupagus.reload", snuffleupagusReloadHandler)
}
