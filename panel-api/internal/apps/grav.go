package apps

// Grav is the descriptor for Grav CMS — the 7th app in the catalog
// and the second flat-file CMS (after DokuWiki). RequiresDB=false; the
// agent extracts the grav-admin zip distribution (which bundles core +
// admin plugin) and creates the admin user via Grav's login-plugin
// CLI: `bin/plugin login newuser`.
//
// Grav publishes only .zip distributions (no .tar.gz), so install.sh
// pulls in `unzip` alongside tar/bzip2.
//
// Clone is intentionally empty for now: a clean Grav clone needs a
// full filesystem rsync of user/ + cache/ + logs/ which the
// wp-clone shape doesn't translate. Defer until a real ask shows up.
var Grav = App{
	Name:                 "grav",
	DisplayName:          "Grav",
	Icon:                 "ApiOutlined",
	Description:          "Modern flat-file CMS — Markdown content, Twig templates, no database, perfect for blogs and docs sites.",
	DefaultSubdirectory:  "",
	RequiresDB:           false,
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
			Default:     "My Grav Site",
			Description: "Public site title; written into user/config/site.yaml.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by Grav for password resets and as the default From-address.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. Grav requires ≥8 chars; the ULID generator satisfies that.",
		},
		"admin_full_name": {
			Type:        "string",
			Required:    false,
			Default:     "Site Administrator",
			Description: "Display name for the admin account; shown in the admin UI top-right.",
		},
	},
}
