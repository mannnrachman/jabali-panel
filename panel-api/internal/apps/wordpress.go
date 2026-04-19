package apps

// WordPress is the descriptor for the existing one-click WordPress
// installer. It records the agent commands the API has been calling
// since M10 and the install-form fields that today live as ad-hoc
// JSON tags on the WP install handler. Step 3 hooks the generic
// /applications routes to this; step 5 wires the UI picker.
//
// Pattern is intentionally not set on admin_username — WordPress
// itself accepts a wide range of usernames and the agent's installer
// already rejects anything wp-cli refuses. Tightening here only adds
// a parallel rule to keep in sync.
var WordPress = App{
	Name:                 "wordpress",
	DisplayName:          "WordPress",
	Icon:                 "WordPressOutlined",
	Description:          "The most popular open-source CMS — blogs, marketing sites, online stores via WooCommerce.",
	DefaultSubdirectory:  "",
	RequiresDB:           true,
	SupportedPHPVersions: nil,
	// M19 dispatcher commands (panel-agent/internal/commands/app_dispatch.go).
	// The legacy wordpress.* commands stay registered on the agent for one
	// release in case a stale panel build is rolled back; new traffic goes
	// through app.* with app_type carried in the params.
	AgentInstallCmd:      "app.install",
	AgentDeleteCmd:       "app.delete",
	AgentCloneCmd:        "app.clone",
	InstallParamSchema: map[string]ParamSpec{
		"admin_username": {
			Type:        "string",
			Required:    true,
			Description: "WordPress administrator login.",
		},
		"admin_email": {
			Type:        "email",
			Required:    true,
			Description: "Administrator email — used by WP for password resets and admin notices.",
		},
		"admin_password": {
			Type:        "password",
			Required:    true,
			Description: "Initial administrator password. Stored only in WP's user table.",
		},
		"site_title": {
			Type:        "string",
			Required:    true,
			Default:     "My WordPress Site",
			Description: "Public site title (editable from WP admin afterwards).",
		},
		"locale": {
			Type:        "string",
			Required:    false,
			Default:     "en_US",
			Description: "Two-part locale code (e.g. en_US, he_IL).",
		},
	},
}

// RegisterDefaults registers every first-party app descriptor on r.
// Called by app.NewWithDeps at startup. New apps are added by appending
// a Register call here.
func RegisterDefaults(r *Registry) error {
	if err := r.Register(WordPress); err != nil {
		return err
	}
	if err := r.Register(DokuWiki); err != nil {
		return err
	}
	return nil
}
