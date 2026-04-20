package hydraclient

import (
	"context"
	"net/http"
	"net/url"
)

// LoginRequest is Hydra's view of a pending /oauth2/auth flow that
// Hydra has delegated to our /oauth2-login handler. The handler reads
// this to find out which client is asking and what scopes are
// requested so it can surface the right consent UI (or auto-accept
// for trusted clients).
type LoginRequest struct {
	Challenge       string   `json:"challenge"`
	Skip            bool     `json:"skip"`
	Subject         string   `json:"subject"`
	Client          Client2  `json:"client"`
	RequestURL      string   `json:"request_url"`
	RequestedScope  []string `json:"requested_scope"`
	SessionID       string   `json:"session_id"`
	RequestedAudience []string `json:"requested_access_token_audience"`
}

// Client2 is the trimmed client view embedded inside Login/Consent
// requests. Not to be confused with the top-level hydraclient.Client
// (our Go HTTP helper) — this one is Hydra's record. Named with a 2
// suffix to avoid the collision at callsite.
type Client2 struct {
	ClientID   string         `json:"client_id"`
	ClientName string         `json:"client_name"`
	Metadata   map[string]any `json:"metadata"`
	Scope      string         `json:"scope"`
}

// Trusted extracts metadata.trusted as a bool. Returns false if the
// key is missing or the wrong type — fail closed, never auto-consent
// on a malformed record. Called by the /oauth2-consent handler to
// decide whether to show the consent UI or auto-accept.
func (c Client2) Trusted() bool {
	v, ok := c.Metadata["trusted"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// ConsentRequest is Hydra's view of a pending consent step that Hydra
// has delegated to our /oauth2-consent handler. After a successful
// login-accept, Hydra redirects the browser back with a
// consent_challenge; the handler uses this to render the AntD consent
// card with the correct client name + requested scopes.
type ConsentRequest struct {
	Challenge              string   `json:"challenge"`
	Skip                   bool     `json:"skip"`
	Subject                string   `json:"subject"`
	Client                 Client2  `json:"client"`
	RequestedScope         []string `json:"requested_scope"`
	RequestedAudience      []string `json:"requested_access_token_audience"`
	LoginChallenge         string   `json:"login_challenge"`
	LoginSessionID         string   `json:"login_session_id"`
}

// AcceptLoginInput shapes the PUT body for login-accept. See the
// Decision 5 invariant on IdentityProviderSessionID — the login
// handler MUST populate it with a hash of the Kratos session cookie
// value so Hydra's session-revocation cascade can invalidate derived
// tokens when the Kratos session is revoked.
type AcceptLoginInput struct {
	// Subject is the panel users.id (ULID). This becomes the `sub`
	// claim in ID tokens.
	Subject string

	// IdentityProviderSessionID — Decision 5 invariant.
	//
	// MUST be a deterministic, stable, non-empty string that uniquely
	// identifies the upstream (Kratos) session. The oauth2_flow
	// handler populates it with SHA-256(ory_kratos_session cookie
	// value) so (a) it's stable across retries and (b) it never
	// leaks the raw cookie to Hydra's DB.
	//
	// When Kratos revokes that session, panel-api fires
	// `RevokeSessionsByIdentityProviderSessionID` so Hydra kills all
	// tokens derived from the now-dead upstream session.
	//
	// Tests assert this field is non-empty on every call — empty
	// would silently disable the cascade.
	IdentityProviderSessionID string

	// Remember=true persists the login consent across Hydra's
	// `ttl.login_consent_request` window — a user re-visiting the
	// client within that window skips re-auth. Defaults to true
	// because most callers want the common single-session UX.
	Remember bool

	// RememberFor is the lifetime of the "remember me" marker. Zero
	// means "until the Kratos session ends" — which is what we want
	// because it binds remember-lifetime to upstream session lifetime
	// and keeps Decision 5's cascade effective.
	RememberFor int
}

// AcceptLoginRequest tells Hydra to complete the login challenge.
// Called from /oauth2-login after the handler has validated the
// Kratos session and resolved the panel user. Returns the redirect
// URL Hydra wants the browser to follow next (usually back to
// /oauth2-consent with a consent_challenge).
//
// Decision 5 invariant:  the handler MUST set
// IdentityProviderSessionID so session revocation cascades. Missing
// it doesn't break the happy path but silently disables the
// revocation cascade — a logged-out user can replay tokens until
// natural TTL (30m for access, 30d for refresh).
func (c *Client) AcceptLoginRequest(ctx context.Context, challenge string, in AcceptLoginInput) (redirectTo string, err error) {
	body := map[string]any{
		"subject":                      in.Subject,
		"identity_provider_session_id": in.IdentityProviderSessionID,
		"remember":                     in.Remember,
		"remember_for":                 in.RememberFor,
	}
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	q := url.Values{}
	q.Set("login_challenge", challenge)
	path := "/admin/oauth2/auth/requests/login/accept?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return "", err
	}
	return out.RedirectTo, nil
}

// RejectLoginInput lets the handler deny a login — used when we
// detect a mismatch between the OIDC client's subject hint and the
// current Kratos session, or when the user hits Cancel on any
// interstitial step.
type RejectLoginInput struct {
	Error            string // "access_denied" for user-initiated deny
	ErrorDescription string // human-readable detail; safe to show client
	StatusCode       int    // 403 by convention
}

// RejectLoginRequest fails the login challenge at Hydra. Hydra
// redirects the browser back to the OIDC client with a standard
// OAuth 2 error response.
func (c *Client) RejectLoginRequest(ctx context.Context, challenge string, in RejectLoginInput) (redirectTo string, err error) {
	body := map[string]any{
		"error":             in.Error,
		"error_description": in.ErrorDescription,
		"status_code":       in.StatusCode,
	}
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	q := url.Values{}
	q.Set("login_challenge", challenge)
	path := "/admin/oauth2/auth/requests/login/reject?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return "", err
	}
	return out.RedirectTo, nil
}

// GetLoginRequest fetches the metadata for a pending login
// challenge. Called by /oauth2-login so the handler can render the
// right content (client name, requested scopes) and decide whether
// to auto-accept or kick to /login first.
func (c *Client) GetLoginRequest(ctx context.Context, challenge string) (LoginRequest, error) {
	q := url.Values{}
	q.Set("login_challenge", challenge)
	path := "/admin/oauth2/auth/requests/login?" + q.Encode()
	var out LoginRequest
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return LoginRequest{}, err
	}
	return out, nil
}

// GetConsentRequest fetches the metadata for a pending consent
// challenge. /oauth2-consent uses it to render the AntD card (client
// name, requested scopes) and to make the "trusted = auto-accept"
// decision.
func (c *Client) GetConsentRequest(ctx context.Context, challenge string) (ConsentRequest, error) {
	q := url.Values{}
	q.Set("consent_challenge", challenge)
	path := "/admin/oauth2/auth/requests/consent?" + q.Encode()
	var out ConsentRequest
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return ConsentRequest{}, err
	}
	return out, nil
}

// AcceptConsentInput shapes the PUT body for consent-accept.
type AcceptConsentInput struct {
	// GrantScope is the set of scopes the user approved. For trusted
	// clients (auto-accept path) this is the requested set verbatim;
	// for untrusted (UI path) it's whatever the user checked off.
	// MUST be a subset of the requested_scope — Hydra rejects an
	// accept that grants unrequested scopes.
	GrantScope []string

	// Session lets us attach OIDC `id_token` claims (name, email,
	// etc.) and access-token claims (jabali.is_admin). Populated by
	// the handler from the panel user record after whoami.
	Session ConsentSession

	// Remember + RememberFor persist the consent across the
	// remember_for window. For trusted clients this has no effect
	// (trust path is independent); for untrusted it controls whether
	// the user re-sees the consent card on their next visit.
	Remember    bool
	RememberFor int
}

// ConsentSession carries the claim bag Hydra will mint into the
// tokens. Only the fields our plan §0 Decision 12 advertises are
// populated; everything else is deliberately omitted rather than
// defaulted, so adding a new claim requires a deliberate code change.
type ConsentSession struct {
	IDToken     map[string]any `json:"id_token,omitempty"`
	AccessToken map[string]any `json:"access_token,omitempty"`
}

// AcceptConsentRequest tells Hydra to issue tokens for an approved
// consent. Returns the redirect URL Hydra wants the browser to
// follow next (the OAuth 2 redirect_uri with a `code` parameter).
func (c *Client) AcceptConsentRequest(ctx context.Context, challenge string, in AcceptConsentInput) (redirectTo string, err error) {
	body := map[string]any{
		"grant_scope":                  in.GrantScope,
		"session":                      in.Session,
		"remember":                     in.Remember,
		"remember_for":                 in.RememberFor,
	}
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	q := url.Values{}
	q.Set("consent_challenge", challenge)
	path := "/admin/oauth2/auth/requests/consent/accept?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return "", err
	}
	return out.RedirectTo, nil
}

// RejectConsentInput is the user-deny case.
type RejectConsentInput struct {
	Error            string
	ErrorDescription string
	StatusCode       int
}

// RejectConsentRequest fails the consent challenge. Hydra redirects
// the browser back to the OIDC client with `error=access_denied` (or
// whatever Error value we set).
func (c *Client) RejectConsentRequest(ctx context.Context, challenge string, in RejectConsentInput) (redirectTo string, err error) {
	body := map[string]any{
		"error":             in.Error,
		"error_description": in.ErrorDescription,
		"status_code":       in.StatusCode,
	}
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	q := url.Values{}
	q.Set("consent_challenge", challenge)
	path := "/admin/oauth2/auth/requests/consent/reject?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodPut, path, body, &out); err != nil {
		return "", err
	}
	return out.RedirectTo, nil
}
