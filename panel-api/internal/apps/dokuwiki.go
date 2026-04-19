package apps

// DokuWiki is the descriptor for the flat-file DokuWiki CMS. Picked
// as the second app specifically because it exercises the framework's
// RequiresDB=false short-circuit (skip the entire panel-row + agent
// db.create / db_user.create / db_user.grant chain) without dragging
// in a heavier CLI installer like wp-cli.
//
// The license enum exercises ParamSpec.Type="enum" — the first
// non-trivial enum in the catalog. Adding it here is what proves the
// install-modal renderer in step 5a actually swaps the field block
// when the picked descriptor has different params.
//
// No clone command: a flat-file rsync of conf/ + data/ pages would
// leave wiki ACLs and registered users intact across the destination,
// which is rarely what the user wants. Defer until a real ask shows
// up; until then the Clone button is hidden in the UI for DokuWiki.
var DokuWiki = App{
	Name:                 "dokuwiki",
	DisplayName:          "DokuWiki",
	Icon:                 "BookOutlined",
	Description:          "A simple flat-file wiki — no database, plain-text pages, perfect for documentation and team knowledge bases.",
	DefaultSubdirectory:  "wiki",
	RequiresDB:           false,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	InstallParamSchema: map[string]ParamSpec{
		"site_title": {
			Type:        "string",
			Required:    true,
			Default:     "My DokuWiki",
			Description: "Public wiki title shown in the page header.",
		},
		"admin_username": {
			Type:        "string",
			Required:    true,
			Default:     "admin",
			Description: "Initial wiki administrator login.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by DokuWiki for password resets.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated.",
		},
		"license": {
			Type:     "enum",
			Required: true,
			Values: []string{
				"cc-by-sa",
				"cc-by-nc-sa",
				"public-domain",
				"gpl",
				"none",
			},
			Default:     "cc-by-sa",
			Description: "License applied to wiki contributions. cc-by-sa matches the DokuWiki default.",
		},
	},
}
