package apps

// ConcreteCMS is the descriptor for Concrete CMS — the 10th app in
// the catalog. RequiresDB=true; install runs the bundled CLI tool
// `concrete/bin/concrete c5:install` which handles DB schema, admin
// user creation, and starting-point content (elemental_blank vs
// elemental_full) in one shot.
//
// Clone is intentionally empty for now: a clean Concrete clone needs
// duplicate DB + an `application/files/` rsync. Defer until ask.
var ConcreteCMS = App{
	Name:                 "concrete",
	DisplayName:          "Concrete CMS",
	Icon:                 "ApiOutlined",
	Description:          "Open-source CMS focused on in-context editing — non-developers can rearrange page blocks live without entering an admin section.",
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
			Default:     "My Concrete Site",
			Description: "Public site title; passed as --site to Concrete's CLI installer.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by Concrete for password resets.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. Concrete requires ≥5 chars; the ULID generator satisfies that.",
		},
		"starting_point": {
			Type:     "enum",
			Required: false,
			Values: []string{
				"elemental_blank",
				"elemental_full",
			},
			Default:     "elemental_full",
			Description: "Starting-point package: 'elemental_full' seeds a demo site with sample pages; 'elemental_blank' is empty.",
		},
		"locale": {
			Type:        "string",
			Required:    false,
			Default:     "en_US",
			Description: "Site locale (e.g. en_US, de_DE).",
		},
	},
}
