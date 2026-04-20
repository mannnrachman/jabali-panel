// Package hydraclient wraps Ory Hydra's admin API. scope_labels.go is
// vendored in Wave A (Step 1) ahead of the rest of the package so the
// consent screen (Step 5) and the consent handler (Step 4) can share a
// single source of truth for human-readable scope descriptions.
//
// Rule: panel-ui never hardcodes scope labels. The consent read endpoint
// (/api/v1/oauth2/consent/:challenge) returns labels from this map. A
// unit test asserts the map covers every scope Hydra is configured to
// advertise in /.well-known/openid-configuration — if the config adds a
// new scope and this map doesn't, the test fails loudly rather than the
// UI falling back to the raw scope name (which would under-describe
// what the user is about to approve).
package hydraclient

// ScopeLabel renders a scope ID as a short noun phrase plus a one-line
// description. Keep the phrasing consistent: noun phrase first, then a
// sentence starting with "Let this app ..." so every scope reads the
// same way when stacked on the consent card.
type ScopeLabel struct {
	// Scope is the literal OAuth 2 scope identifier (e.g. "openid").
	Scope string
	// Short is the 1–3 word noun phrase used for list items.
	Short string
	// Long is the one-line sentence shown under the noun. Always starts
	// with "Let this app ..." so consent copy stays predictable.
	Long string
}

// ScopeLabels enumerates every scope Hydra is configured to issue in
// M16. Adding a scope here without adding it to hydra.yml (or vice
// versa) will trip the TestScopeLabels_CoverHydraAdvertisedScopes test.
//
// Order matters: the consent card renders scopes in the order listed
// here, so a user sees "Your identity" before "Email" before "Profile"
// rather than alphabetical. This matches every major OIDC consent UI
// (Google, GitHub, Auth0) — identity first, contact second, everything
// else after.
var ScopeLabels = []ScopeLabel{
	{
		Scope: "openid",
		Short: "Your identity",
		Long:  "Let this app see a stable identifier for your Jabali account so it can remember you between visits.",
	},
	{
		Scope: "email",
		Short: "Email address",
		Long:  "Let this app see the email address on your Jabali account.",
	},
	{
		Scope: "profile",
		Short: "Profile information",
		Long:  "Let this app see your display name and other public profile fields from your Jabali account.",
	},
}

// LabelFor returns the ScopeLabel for a given scope ID. Returns
// (label, true) on hit and (ScopeLabel{}, false) on miss. Callers must
// treat a miss as a fail-loud condition — do NOT fall back to rendering
// the raw scope string, because a scope whose consequences we haven't
// written copy for is a scope we haven't reviewed for least-privilege.
func LabelFor(scope string) (ScopeLabel, bool) {
	for _, l := range ScopeLabels {
		if l.Scope == scope {
			return l, true
		}
	}
	return ScopeLabel{}, false
}

// KnownScopes returns just the scope IDs, in the same order as
// ScopeLabels. Handy for config validation and test assertions.
func KnownScopes() []string {
	out := make([]string, len(ScopeLabels))
	for i, l := range ScopeLabels {
		out[i] = l.Scope
	}
	return out
}
