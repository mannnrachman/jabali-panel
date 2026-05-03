// Package commands — security_snuffleupagus.go (M41, ADR-0088)
//
// Agent-side handlers for the Snuffleupagus PHP-hardening module.
// snuffleupagus.status         — reports loaded .so per PHP minor + active rule SHA
// snuffleupagus.reload         — graceful-reload every per-user FPM pool after a
//                                rule-file rewrite (called by the panel reconciler)
// snuffleupagus.incident_tail  — internal: agent-bg journalctl tail loop
//
// Implementation lands in Wave D (steps 6-7). This file is the registration
// stub so all subsequent waves can slot handlers in without route renaming.
package commands

import (
	"context"
	"encoding/json"
)

type snuffleupagusStatusResponse struct {
	Enabled            bool                          `json:"enabled"`
	Mode               string                        `json:"mode"`
	ActiveRulesSha256  string                        `json:"active_rules_sha256,omitempty"`
	PhpVersionsLoaded  []snuffleupagusPhpVersionStat `json:"php_versions_loaded"`
}

type snuffleupagusPhpVersionStat struct {
	Minor       string `json:"minor"`
	ExtensionSO string `json:"extension_so"`
	Loaded      bool   `json:"loaded"`
}

func snuffleupagusStatusHandler(_ context.Context, _ json.RawMessage) (any, error) {
	// Wave D fills this in.
	return snuffleupagusStatusResponse{
		Enabled:           false,
		Mode:              "off",
		PhpVersionsLoaded: []snuffleupagusPhpVersionStat{},
	}, nil
}

type snuffleupagusReloadResponse struct {
	Pools []snuffleupagusPoolReloadResult `json:"pools"`
}

type snuffleupagusPoolReloadResult struct {
	Pool   string `json:"pool"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func snuffleupagusReloadHandler(_ context.Context, _ json.RawMessage) (any, error) {
	// Wave D fills this in.
	return snuffleupagusReloadResponse{Pools: []snuffleupagusPoolReloadResult{}}, nil
}

func init() {
	Default.Register("snuffleupagus.status", snuffleupagusStatusHandler)
	Default.Register("snuffleupagus.reload", snuffleupagusReloadHandler)
}
