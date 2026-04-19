package apps

// OpenCart is the descriptor for OpenCart 4 — the 11th app in the
// catalog and the first e-commerce platform. RequiresDB=true; install
// runs `php install/cli_install.php install` with the DB + admin args
// so the user never sees the web installer.
//
// Clone is intentionally empty for now: a clean OpenCart clone needs
// duplicate DB + image/* rsync + admin URL rewrite. Defer.
var OpenCart = App{
	Name:                 "opencart",
	DisplayName:          "OpenCart",
	Icon:                 "ShoppingCartOutlined",
	Description:          "Open-source e-commerce platform — product catalog, carts, checkout, order management, and a marketplace of contributed extensions.",
	DefaultSubdirectory:  "shop",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "",
	InstallParamSchema: map[string]ParamSpec{
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by OpenCart for password resets and as the default From-address for transactional emails.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. OpenCart requires ≥4 chars; the ULID generator satisfies that.",
		},
	},
}
