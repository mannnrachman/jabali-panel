package apps

// GLPI is the descriptor for GLPI — the 16th app in the catalog and
// the first IT asset management tool. RequiresDB=true; install runs
// `php bin/console glpi:database:install` to materialise the schema,
// then a one-shot inline PHP script to rotate the default `glpi/glpi`
// admin credentials and disable the other default accounts (tech,
// normal, post-only) — leaving them with their default passwords
// would be a CVE waiting to happen on a public-facing install.
//
// GLPI ships .htaccess files in files/ and config/ that deny direct
// web access, so an in-tree install (everything under docroot) is
// safe enough on nginx with a docroot-rooted vhost. Apps that
// genuinely require an out-of-tree data dir (Moodle does) use the
// M19 managed-data-dir framework instead.
//
// Clone is intentionally empty: a clean GLPI clone needs duplicate
// DB + files/ rsync + config/config_db.php rewrite. Defer.
var GLPI = App{
	Name:                 "glpi",
	DisplayName:          "GLPI",
	Icon:                 "ToolOutlined",
	Description:          "Open-source IT asset management — inventory, helpdesk ticketing, knowledge base, license tracking. Long-time favourite for SMB IT departments.",
	DefaultSubdirectory:  "glpi",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	InstallParamSchema: map[string]ParamSpec{
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used for the rotated admin account's contact info.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password (replaces the default 'glpi/glpi' credentials). Leave blank to have one generated.",
		},
		"language": {
			Type:        "string",
			Required:    false,
			Default:     "en_GB",
			Description: "UI language (en_GB, fr_FR, de_DE, …). GLPI ships every language pack in core.",
		},
	},
}
