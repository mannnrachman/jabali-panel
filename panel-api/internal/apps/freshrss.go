package apps

// FreshRSS is the descriptor for FreshRSS — the 8th app in the catalog
// and the first feed reader. RequiresDB=true (MariaDB); install runs
// FreshRSS's two CLI scripts: cli/do-install.php to write the data
// store and cli/create-user.php to provision the admin account.
//
// Clone is intentionally empty for now: a clean FreshRSS clone needs
// duplicate DB + a `data/` rsync (per-user feed caches and OPML
// imports). Defer until a real ask shows up.
var FreshRSS = App{
	Name:                 "freshrss",
	DisplayName:          "FreshRSS",
	Icon:                 "ApiOutlined",
	Description:          "Self-hosted RSS aggregator — multi-user, OPML import, web reader, mobile-friendly.",
	DefaultSubdirectory:  "rss",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	// admin_username intentionally NOT in this schema — see WordPress
	// descriptor for the rationale.
	InstallParamSchema: map[string]ParamSpec{
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — FreshRSS uses it for password resets.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. FreshRSS requires ≥7 chars; the ULID generator satisfies that.",
		},
		"language": {
			Type:        "string",
			Required:    false,
			Default:     "en",
			Description: "UI language code (en, fr, de, …). FreshRSS ships every language pack in core; no post-install download needed.",
		},
	},
}
