package apps

// MediaWiki is the descriptor for the MediaWiki CMS — the platform
// behind Wikipedia. Picked as the third app specifically because it
// exercises a heavier per-app install flow than WordPress's wp-cli:
// the agent installer runs MediaWiki's own `php maintenance/install.php`
// CLI with pre-supplied credentials so the user never sees the web
// installer wizard, then drops the generated LocalSettings.php into
// place.
//
// Cloning a MediaWiki install means duplicating the database AND the
// images/ directory AND the LocalSettings.php — the wp-clone shape
// doesn't translate. Defer until a real ask shows up; AgentCloneCmd
// stays empty so the Clone button is hidden in the UI.
var MediaWiki = App{
	Name:                 "mediawiki",
	DisplayName:          "MediaWiki",
	Icon:                 "BookOutlined",
	Description:          "The wiki engine behind Wikipedia — full revision history, namespaces, extensions, scales to encyclopaedias.",
	DefaultSubdirectory:  "wiki",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	// admin_username intentionally NOT in this schema — the API
	// generates a 6-letter username server-side and uppercases the
	// first letter to satisfy MediaWiki's username-must-start-with-
	// uppercase rule. See WordPress descriptor for the rationale.
	InstallParamSchema: map[string]ParamSpec{
		"site_title": {
			Type:        "string",
			Required:    true,
			Default:     "My MediaWiki",
			Description: "Public wiki title; passed as the first positional argument to MediaWiki's CLI installer.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by MediaWiki for password resets.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. MediaWiki requires ≥10 chars; the generator produces a ULID which satisfies that.",
		},
		"language": {
			Type:        "string",
			Required:    false,
			Default:     "en",
			Description: "Wiki content language (ISO 639 code, e.g. en, he, fr).",
		},
	},
}
