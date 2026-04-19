package apps

// Joomla is the descriptor for the Joomla 5 CMS — the 5th app in the
// catalog. RequiresDB=true; install runs Joomla's headless CLI
// (`php installation/joomla.php install`) so the user never sees the
// web installer wizard. After install completes, the agent removes
// the `installation/` directory which Joomla itself refuses to load
// past until cleared.
//
// Clone is intentionally empty: a clean Joomla clone needs duplicate
// DB + an `images/` rsync + a configuration.php rewrite for the new
// host. wp-clone's shape doesn't translate. AgentCloneCmd stays empty
// so the Clone button is hidden in the UI; revisit when there's a
// real ask.
var Joomla = App{
	Name:                 "joomla",
	DisplayName:          "Joomla",
	Icon:                 "AppstoreOutlined",
	Description:          "Open-source CMS — multilingual core, fine-grained ACL, large extension marketplace; the long-time WordPress alternative.",
	DefaultSubdirectory:  "",
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
			Default:     "My Joomla Site",
			Description: "Public site title; passed as --site-name to Joomla's CLI installer.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by Joomla for password resets and admin notices.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. Joomla requires ≥12 chars; the ULID generator satisfies that.",
		},
		"admin_full_name": {
			Type:        "string",
			Required:    false,
			Default:     "Super User",
			Description: "Display name for the Super User account. Joomla shows this in admin instead of the login username.",
		},
	},
}
