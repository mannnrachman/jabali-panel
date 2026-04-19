package apps

// Moodle is the descriptor for Moodle LMS — the 15th app in the
// catalog and the first LMS. RequiresDB=true; install runs Moodle's
// admin/cli/install.php which materialises the schema and creates the
// admin user.
//
// Moodle MUST have a writable data directory (moodledata) OUTSIDE the
// docroot — Moodle itself refuses to install with --dataroot=<inside-
// docroot> for the documented security reason that the dir holds
// user-uploaded files plus session state and must not be web-served.
// The agent uses the M19 managed-data-dir framework (computeManaged
// DataDir / ensureManagedDataDir / removeManagedDataDir) to land the
// dir at /home/<user>/<install_id>-data/ — see the helpers in
// panel-agent/internal/commands/managed_data_dir.go.
//
// Clone is intentionally empty for now: a clean Moodle clone needs
// duplicate DB + moodledata rsync + config.php URL/dataroot rewrite.
// Defer until ask.
var Moodle = App{
	Name:                 "moodle",
	DisplayName:          "Moodle",
	Icon:                 "ApiOutlined",
	Description:          "Open-source learning management system — courses, quizzes, gradebook, used by universities and corporate training departments worldwide.",
	DefaultSubdirectory:  "lms",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	InstallParamSchema: map[string]ParamSpec{
		"site_title": {
			Type:        "string",
			Required:    true,
			Default:     "My Moodle Site",
			Description: "Public site full name; passed to admin/cli/install.php as --fullname.",
		},
		"site_short_name": {
			Type:        "string",
			Required:    false,
			Default:     "Moodle",
			Description: "Short site name shown in the navigation breadcrumb. Defaults to a stripped version of site_title when blank.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by Moodle for password resets and as the default From-address for forum digests.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. Moodle requires ≥8 chars by default.",
		},
		"language": {
			Type:        "string",
			Required:    false,
			Default:     "en",
			Description: "UI language code (en, fr, de, …). Only the English language pack ships in the upstream tarball; other packs need to be added post-install via Site administration → Language packs.",
		},
	},
}
