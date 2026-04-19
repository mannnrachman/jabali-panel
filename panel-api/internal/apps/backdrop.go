package apps

// Backdrop is the descriptor for Backdrop CMS — the 14th app in the
// catalog. RequiresDB=true; Backdrop is a Drupal 7 fork that focuses
// on simplicity for small-to-medium sites. Install runs the bee CLI
// tool's `site-install` command (bee is the Drupal-drush analogue
// for Backdrop), which the agent downloads as a single-file PHP
// script per install (no system-wide install needed).
//
// Clone is intentionally empty for now: a clean Backdrop clone needs
// duplicate DB + files/ rsync + settings.php hash_salt rotation.
// Defer until ask.
var Backdrop = App{
	Name:                 "backdrop",
	DisplayName:          "Backdrop CMS",
	Icon:                 "ApiOutlined",
	Description:          "A Drupal 7 fork — same data model and module API, simpler admin and lighter resource footprint. Aimed at small businesses and non-profits priced out of Drupal 9+ upgrades.",
	DefaultSubdirectory:  "",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	InstallParamSchema: map[string]ParamSpec{
		"site_title": {
			Type:        "string",
			Required:    true,
			Default:     "My Backdrop Site",
			Description: "Public site title; passed to bee site-install as --site-name.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by Backdrop for password resets.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated.",
		},
		"profile": {
			Type:     "enum",
			Required: false,
			Values: []string{
				"standard",
				"minimal",
			},
			Default:     "standard",
			Description: "Backdrop install profile. 'standard' seeds a default content type set; 'minimal' is a bare skeleton.",
		},
	},
}
