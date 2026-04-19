package apps

// AbanteCart is the descriptor for AbanteCart — the 12th app in the
// catalog and the second e-commerce platform after OpenCart. The two
// share a heritage (AbanteCart began as an OpenCart fork in 2010) so
// the install shape is similar: extract zip, run a CLI install
// script in install/, then remove the install directory.
//
// Clone is intentionally empty for now: a clean AbanteCart clone
// needs duplicate DB + image/* rsync + admin URL rewrite. Defer.
var AbanteCart = App{
	Name:                 "abantecart",
	DisplayName:          "AbanteCart",
	Icon:                 "ShoppingCartOutlined",
	Description:          "Open-source e-commerce platform — fully featured store with built-in CMS, downloadable products, multi-currency, and a marketplace of paid extensions.",
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
			Description: "Administrator email — used by AbanteCart for password resets and as the default From-address for transactional emails.",
		},
		"admin_password": {
			Type:        "password",
			Required:    false,
			Description: "Initial admin password. Leave blank to have one generated. AbanteCart requires ≥7 chars; the ULID generator satisfies that.",
		},
	},
}
