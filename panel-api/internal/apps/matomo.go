package apps

// Matomo is the descriptor for Matomo Analytics — the 9th app in the
// catalog and the first analytics tool. RequiresDB=true; install
// pre-writes config/config.ini.php with the DB credentials, then runs
// `php console core:install` to materialise the schema, then
// `php console user:create --user-superuser` to provision the admin.
//
// First-website setup (the wizard's last step on a fresh web install)
// is intentionally NOT automated here — the user picks their tracking
// site URL + name from the Matomo admin once logged in. Pre-creating
// it would require a `first_website_url` param the user might not
// have settled on yet.
//
// Clone is intentionally empty for now: a clean Matomo clone needs
// duplicate DB + config rewrite for the new server URL. Defer.
var Matomo = App{
	Name:                 "matomo",
	DisplayName:          "Matomo",
	Icon:                 "BarChartOutlined",
	Description:          "Open-source web analytics — privacy-first, GDPR-compliant, the self-hosted alternative to Google Analytics.",
	DefaultSubdirectory:  "analytics",
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
			Description: "Administrator email — used by Matomo for password resets and weekly report digests.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. Matomo requires ≥6 chars; the ULID generator satisfies that.",
		},
	},
}
