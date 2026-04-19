package apps

// Drupal is the descriptor for the Drupal 10 CMS — the fourth app
// in the catalog. Like MediaWiki it's RequiresDB=true and ships with
// a CLI installer (drush site:install). Unlike MediaWiki, Drupal needs
// composer present at install time to pull drush into the install
// path's vendor/ tree before drush can drive the install.
//
// Composer is added to install.sh's apt line so a fresh host can run
// drupal installs without out-of-band setup. drush is NOT a system
// package — it's per-install, scoped to vendor/bin/drush in each
// docroot, so multiple Drupal installs can carry different drush
// versions without colliding.
//
// Clone is intentionally empty for now: a clean Drupal clone needs
// duplicate DB + full sites/default/files copy + a settings.php
// rewrite for the new domain. wp-clone's shape doesn't translate
// cleanly. AgentCloneCmd stays empty so the Clone button is hidden
// in the UI; revisit when there's a real ask.
var Drupal = App{
	Name:                 "drupal",
	DisplayName:          "Drupal",
	Icon:                 "ApiOutlined",
	Description:          "Enterprise-grade open-source CMS — content modeling, taxonomies, multilingual, and the contrib module ecosystem.",
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
			Default:     "My Drupal Site",
			Description: "Public site title shown in the page header; passed as drush site:install --site-name.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by Drupal for password resets and admin notices.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated.",
		},
		"site_mail": {
			Type:        "email",
			Required:    false,
			Description: "From-address for outbound email (defaults to admin_email when blank).",
		},
		"profile": {
			Type:     "enum",
			Required: false,
			Values: []string{
				"standard",
				"minimal",
				"demo_umami",
			},
			Default:     "standard",
			Description: "Drupal install profile. 'standard' is the default editor-friendly stack; 'minimal' gives a bare site for custom builds.",
		},
	},
}
