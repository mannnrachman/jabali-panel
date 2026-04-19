package apps

// PhpBB is the descriptor for phpBB 3.3 — the 6th app in the catalog
// and the first community/forum software. RequiresDB=true; install
// writes a YAML config to a temp path and runs phpBB's headless CLI
// (`php install/app.php install <config>`).
//
// Clone is intentionally empty for now: a clean phpBB clone needs a
// duplicate DB + an `images/`, `files/`, and `store/` rsync + a
// rewrite of every cookie/board URL in config_text. wp-clone's shape
// doesn't translate cleanly. AgentCloneCmd stays empty so the Clone
// button is hidden in the UI; revisit when there's a real ask.
var PhpBB = App{
	Name:                 "phpbb",
	DisplayName:          "phpBB",
	Icon:                 "MessageOutlined",
	Description:          "Mature open-source forum software — categories, sub-forums, ranks, MOD/extension ecosystem.",
	DefaultSubdirectory:  "forum",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	// admin_username intentionally NOT in this schema — see WordPress
	// descriptor for the rationale.
	InstallParamSchema: map[string]ParamSpec{
		"site_title": {
			Type:        "string",
			Required:    true,
			Default:     "My Forum",
			Description: "Public board title shown in the header; passed to phpBB's CLI installer as board.name.",
		},
		"board_description": {
			Type:        "string",
			Required:    false,
			Default:     "A discussion forum",
			Description: "Tagline shown under the board title on the board index.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — phpBB uses it for password resets and bounce returns.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. phpBB requires ≥6 chars; the ULID generator satisfies that.",
		},
		"language": {
			Type:        "string",
			Required:    false,
			Default:     "en",
			Description: "Board language pack (en, fr, de, …). Only the English pack ships in the upstream tarball; other packs need to be added post-install via the admin control panel.",
		},
	},
}
