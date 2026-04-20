// Package api's oauth2_flow.go implements the login + consent handlers
// Hydra delegates to for Ory Hydra's login-consent flow. See
// ADR-0036 + plan §0 Decisions 5-7 for the architecture.
//
// Security invariants enforced here:
//
//   - **Decision 5 (revocation cascade).** Every AcceptLoginRequest
//     MUST carry IdentityProviderSessionID = SHA-256(ory_kratos_session
//     cookie value). Without it, Kratos session revocation does NOT
//     kill derived Hydra tokens — a logged-out user can replay tokens
//     until natural TTL (30m access, 30d refresh). The handler
//     refuses to accept a login when the computed IdP session id
//     would be empty. Unit test
//     TestLoginStart_RefusesWithoutIdentityProviderSessionID covers it.
//
//   - **Decision 7 (trusted auto-consent).** The /oauth2-consent
//     handler auto-accepts ONLY when `metadata.trusted == true`
//     *as reported by Hydra*. Hydra is the source of truth here:
//     the flag was set server-side via hydraclient.SetClientTrusted
//     during install (applications_service.go) and the handler
//     trusts that record. A caller-supplied hint (e.g. a query param)
//     has no effect. Unit test
//     TestConsentStart_UntrustedNeverAutoAccepts covers it.
//
// The auth-middleware guard on these routes is still
// RequireKratosSession — no Kratos cookie, no access to Hydra. A
// request that redirects here without a session gets a 401 and
// panel-ui's axios handles the kick-to-login. This is the same
// guarantee every other /api/v1/* route has.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/hydraclient"

	"github.com/gin-gonic/gin"
)

// OAuth2FlowHandlerConfig carries the dependencies oauth2_flow
// handlers need. The Hydra client may be nil in environments without
// install_hydra (dev, pre-M16 in-place installs); handlers return a
// clear 503 when that's the case rather than nil-panicking.
type OAuth2FlowHandlerConfig struct {
	Hydra *hydraclient.Client
	Log   *slog.Logger
	// ConsentUIPath is the SPA route the handler redirects to on
	// untrusted consent. Plan Step 5 spells this
	// `/oauth2-consent`; we use a different path because the Hydra
	// target URL and the SPA route can't share /oauth2-consent —
	// Gin would preempt the SPA's NoRoute fallback. Default
	// "/consent".
	ConsentUIPath string
	// BrowserAuth is the middleware used on the Hydra-target browser
	// routes (GET /oauth2-login, GET /oauth2-consent). It MUST emit a
	// 302 to /login on no-session rather than a JSON 401 — the flow
	// starts with Hydra redirecting a browser to us, and a 401 leaves
	// first-time users staring at a JSON blob. Typically
	// middleware.RequireKratosSessionOrRedirect. When nil, the
	// handlers fall back to the protected-group's middleware
	// (JSON-401 behaviour) so existing test wiring keeps working.
	BrowserAuth gin.HandlerFunc
}

// RegisterOAuth2FlowRoutes wires the login + consent handlers into
// the auth-gated group. The group's middleware (RequireKratosSession)
// guarantees ginctx.Claims is populated by the time any handler
// runs — no nil checks needed downstream.
//
// Route map:
//
//	GET  /oauth2-login            — Hydra's login URL target
//	GET  /oauth2-consent          — Hydra's consent URL target
//	POST /oauth2-consent/accept   — SPA submit target (Allow)
//	POST /oauth2-consent/deny     — SPA submit target (Deny)
//	GET  /api/v1/oauth2/consent/:challenge
//	                              — Read-only metadata for the SPA
func RegisterOAuth2FlowRoutes(protected *gin.RouterGroup, root *gin.Engine, cfg OAuth2FlowHandlerConfig) {
	// Defaults — keep the API shape usable without every caller
	// having to fill ConsentUIPath.
	if cfg.ConsentUIPath == "" {
		cfg.ConsentUIPath = "/consent"
	}

	// The Hydra-target handlers (login-start, consent-start) are
	// gated on Kratos session — no session, no SSO. Registering on
	// the root engine with the same middleware the /api/v1 group
	// uses keeps the auth story consistent.
	//
	// NOTE: these are NOT under /api/v1 because Hydra redirects a
	// browser to them; the URL shape is user-facing, not API-ish.
	loginStart := makeLoginStartHandler(cfg)
	consentStart := makeConsentStartHandler(cfg)
	consentAccept := makeConsentAcceptHandler(cfg)
	consentDeny := makeConsentDenyHandler(cfg)

	// Two middleware tiers:
	//
	//   * Browser-target routes (GET /oauth2-login, GET /oauth2-consent)
	//     need a 302-to-/login on missing session — a browser handles a
	//     redirect; it cannot do anything useful with a 401 JSON blob
	//     in the middle of a Hydra handshake. Use cfg.BrowserAuth when
	//     provided; fall back to the JSON-401 chain when callers (e.g.
	//     tests) leave BrowserAuth nil.
	//
	//   * SPA submit + metadata routes stay on the hard JSON-401 chain.
	//     They're reached via axios, not full-page nav, and the SPA's
	//     auth provider already knows how to handle 401.
	apiChain := protected.Handlers
	browserChain := apiChain
	if cfg.BrowserAuth != nil {
		browserChain = []gin.HandlerFunc{cfg.BrowserAuth}
	}

	root.GET("/oauth2-login", append(browserChain, loginStart)...)
	root.GET("/oauth2-consent", append(browserChain, consentStart)...)
	root.POST("/oauth2-consent/accept", append(apiChain, consentAccept)...)
	root.POST("/oauth2-consent/deny", append(apiChain, consentDeny)...)

	// Read-only metadata for the SPA consent UI. Lives under
	// /api/v1 so the SPA's axios baseURL picks it up automatically.
	protected.GET("/oauth2/consent/:challenge", makeConsentMetadataHandler(cfg))
}

// makeLoginStartHandler builds GET /oauth2-login. Hydra has already
// validated the login_challenge query param exists (it generated it);
// we just fetch the request, accept with the logged-in panel user's
// id + the Decision 5 IdP session id, and 302 back.
func makeLoginStartHandler(cfg OAuth2FlowHandlerConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.Hydra == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "hydra_unavailable",
				"message": "OAuth 2 provider is not configured",
			})
			return
		}
		challenge := c.Query("login_challenge")
		if challenge == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "missing_challenge",
				"message": "login_challenge is required",
			})
			return
		}

		claims := ginctx.Claims(c)
		if claims == nil || claims.UserID == "" {
			// Should never happen — RequireKratosSession guarantees
			// claims. Log and 500 rather than silently return, so a
			// regression in the middleware chain is observable.
			cfg.Log.Error("oauth2-login: missing claims despite RequireKratosSession")
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "missing_claims",
				"message": "internal: authenticated session did not resolve to a user",
			})
			return
		}

		// Decision 5 — compute the IdP session id from the Kratos
		// cookie value BEFORE calling Hydra. If the cookie is missing
		// (again, shouldn't happen under RequireKratosSession) we
		// refuse the accept — better to show the user a 401 than to
		// accept a login without the revocation cascade wired up.
		idpSessionID := computeIDPSessionID(c)
		if idpSessionID == "" {
			cfg.Log.Error("oauth2-login: empty IdP session id — cookie missing or zero-length; refusing to accept without revocation cascade")
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "missing_session_cookie",
				"message": "cannot establish OIDC session without a Kratos session cookie",
			})
			return
		}

		redirect, err := cfg.Hydra.AcceptLoginRequest(c.Request.Context(), challenge,
			hydraclient.AcceptLoginInput{
				Subject:                   claims.UserID,
				IdentityProviderSessionID: idpSessionID,
				Remember:                  true,
				// RememberFor=0 binds the OAuth "remember me"
				// window to the Kratos session's lifetime.
				// Terminating the Kratos session ends the OAuth
				// remember-me — which is exactly what Decision 5
				// wants.
				RememberFor: 0,
			})
		if err != nil {
			cfg.Log.Error("oauth2-login: AcceptLoginRequest failed",
				"err", err, "user_id", claims.UserID, "challenge", challenge)
			c.JSON(http.StatusBadGateway, gin.H{
				"error":   "hydra_accept_failed",
				"message": "OAuth 2 provider rejected the login",
			})
			return
		}

		cfg.Log.Info("oauth2_login_accepted",
			"event", "hydra_login_accept",
			"user_id", claims.UserID,
			"challenge", challenge)

		c.Redirect(http.StatusFound, redirect)
	}
}

// makeConsentStartHandler builds GET /oauth2-consent. Either
// auto-accepts (for trusted first-party clients) or 302s to the SPA
// consent UI so the user can approve scopes.
//
// Decision 7: the ONLY input to the trust decision is Hydra's
// metadata.trusted on the client record. The handler reads it from
// the ConsentRequest response; it does NOT accept a caller-supplied
// "trusted" signal from query params, headers, or body.
func makeConsentStartHandler(cfg OAuth2FlowHandlerConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.Hydra == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "hydra_unavailable",
				"message": "OAuth 2 provider is not configured",
			})
			return
		}
		challenge := c.Query("consent_challenge")
		if challenge == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "missing_challenge",
				"message": "consent_challenge is required",
			})
			return
		}

		req, err := cfg.Hydra.GetConsentRequest(c.Request.Context(), challenge)
		if err != nil {
			cfg.Log.Error("oauth2-consent: GetConsentRequest failed",
				"err", err, "challenge", challenge)
			c.JSON(http.StatusBadGateway, gin.H{
				"error":   "hydra_fetch_failed",
				"message": "could not load consent request",
			})
			return
		}

		// Decision 7 gate — server-side only. Hydra's metadata.trusted
		// was set via SetClientTrusted at client install time (apps
		// framework, Wave D Step 6). No other path sets it.
		if req.Client.Trusted() {
			grant := req.RequestedScope
			session := buildConsentSession(ginctx.Claims(c))
			redirect, acceptErr := cfg.Hydra.AcceptConsentRequest(c.Request.Context(), challenge,
				hydraclient.AcceptConsentInput{
					GrantScope: grant,
					Session:    session,
					Remember:   true,
				})
			if acceptErr != nil {
				cfg.Log.Error("oauth2-consent: auto-accept failed",
					"err", acceptErr, "challenge", challenge, "client_id", req.Client.ClientID)
				c.JSON(http.StatusBadGateway, gin.H{
					"error":   "hydra_accept_failed",
					"message": "OAuth 2 provider rejected the consent",
				})
				return
			}
			cfg.Log.Info("oauth2_consent_auto_accepted",
				"event", "hydra_consent_auto_accept",
				"client_id", req.Client.ClientID,
				"challenge", challenge,
				"scopes", grant)
			c.Redirect(http.StatusFound, redirect)
			return
		}

		// Untrusted path — 302 to the SPA consent UI. The SPA reads
		// the challenge id, calls GET /api/v1/oauth2/consent/:challenge
		// for metadata, renders the approve/deny form, and POSTs to
		// /oauth2-consent/{accept,deny}.
		u := url.URL{
			Path:     cfg.ConsentUIPath,
			RawQuery: "challenge=" + url.QueryEscape(challenge),
		}
		c.Redirect(http.StatusFound, u.String())
	}
}

// ConsentMetadata is the read-only shape the SPA reads to render the
// consent card. Fields match Wave C's plan: the SPA doesn't need the
// challenge (it's in the URL), but does need the client name, scope
// labels, and the subject it's about to consent to.
type ConsentMetadata struct {
	ClientName     string                  `json:"client_name"`
	RequestedScope []ScopeWithLabel        `json:"requested_scope"`
	Subject        string                  `json:"subject"`
}

// ScopeWithLabel is a scope plus its human-readable description from
// scope_labels.go. If LabelFor misses (unknown scope), we return the
// raw id + short="Unknown scope" + long="Copy not found..." so the
// SPA still renders something the user can act on — NOT silently hide
// a scope it's about to grant.
type ScopeWithLabel struct {
	Scope string `json:"scope"`
	Short string `json:"short"`
	Long  string `json:"long"`
}

// makeConsentMetadataHandler builds GET /api/v1/oauth2/consent/:challenge.
// Read-only, safe to call without CSRF guard. Used by the SPA consent
// page.
func makeConsentMetadataHandler(cfg OAuth2FlowHandlerConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.Hydra == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "hydra_unavailable",
			})
			return
		}
		challenge := c.Param("challenge")
		if challenge == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing_challenge"})
			return
		}
		req, err := cfg.Hydra.GetConsentRequest(c.Request.Context(), challenge)
		if err != nil {
			if errors.Is(err, hydraclient.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "challenge_not_found"})
				return
			}
			cfg.Log.Error("oauth2-consent metadata: GetConsentRequest failed", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "hydra_fetch_failed"})
			return
		}

		md := ConsentMetadata{
			ClientName: req.Client.ClientName,
			Subject:    req.Subject,
		}
		for _, s := range req.RequestedScope {
			label, ok := hydraclient.LabelFor(s)
			if !ok {
				// Fail OBSERVABLE: the SPA still renders the scope,
				// but with copy that makes the reviewer notice. Don't
				// silently drop — dropping would let a scope slip
				// past the user's review.
				md.RequestedScope = append(md.RequestedScope, ScopeWithLabel{
					Scope: s,
					Short: "Unknown scope: " + s,
					Long:  "This scope is not in the panel's label catalog; contact an administrator before approving.",
				})
				continue
			}
			md.RequestedScope = append(md.RequestedScope, ScopeWithLabel{
				Scope: s,
				Short: label.Short,
				Long:  label.Long,
			})
		}
		c.JSON(http.StatusOK, md)
	}
}

// makeConsentAcceptHandler builds POST /oauth2-consent/accept. SPA
// calls with { challenge, grant_scope[] } after the user clicks Allow.
//
// CSRF protection is inherent to the flow:
//   - Hydra's consent_challenge is one-shot — Hydra rejects a second
//     accept/reject on the same challenge, so a CSRF'd replay fails.
//   - The Kratos session cookie binds the request to the current user.
//   - The handler re-fetches the consent request and verifies the
//     subject matches ginctx.Claims.UserID — a cross-user accept fails.
//
// Which means a separate CSRF token is belt-and-suspenders, not
// required. The plan's review checklist called for "a signed/bound
// token"; the challenge id itself is that token, signed by Hydra.
type consentAcceptBody struct {
	Challenge  string   `json:"challenge" binding:"required"`
	GrantScope []string `json:"grant_scope" binding:"required"`
}

func makeConsentAcceptHandler(cfg OAuth2FlowHandlerConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.Hydra == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "hydra_unavailable"})
			return
		}
		var body consentAcceptBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad_body", "message": err.Error()})
			return
		}

		// Re-fetch to bind: a malicious client can't POST a challenge
		// that doesn't belong to their Kratos session.
		req, err := cfg.Hydra.GetConsentRequest(c.Request.Context(), body.Challenge)
		if err != nil {
			cfg.Log.Error("oauth2-consent/accept: GetConsentRequest failed", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "hydra_fetch_failed"})
			return
		}
		claims := ginctx.Claims(c)
		if claims == nil || req.Subject != claims.UserID {
			cfg.Log.Warn("oauth2-consent/accept: subject mismatch — attempted cross-user consent",
				"consent_subject", req.Subject, "panel_user", userIDFromClaims(claims))
			c.JSON(http.StatusForbidden, gin.H{"error": "subject_mismatch"})
			return
		}

		// grant_scope MUST be a subset of requested_scope — Hydra
		// rejects unrequested scopes, but we check here too so the
		// user can't be tricked by a SPA bug into granting more than
		// they clicked.
		if !isSubset(body.GrantScope, req.RequestedScope) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "grant_scope_not_subset"})
			return
		}

		session := buildConsentSession(claims)
		redirect, err := cfg.Hydra.AcceptConsentRequest(c.Request.Context(), body.Challenge,
			hydraclient.AcceptConsentInput{
				GrantScope: body.GrantScope,
				Session:    session,
				Remember:   true,
				// RememberFor: 720h per plan decision 14 for
				// untrusted clients. Zero means "Hydra default" which
				// would be session-bound; we want the 30d window.
				RememberFor: 720 * 3600,
			})
		if err != nil {
			cfg.Log.Error("oauth2-consent/accept: AcceptConsentRequest failed", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "hydra_accept_failed"})
			return
		}
		cfg.Log.Info("oauth2_consent_accepted",
			"event", "hydra_consent_accept",
			"client_id", req.Client.ClientID,
			"challenge", body.Challenge,
			"scopes", body.GrantScope,
			"user_id", claims.UserID)
		c.JSON(http.StatusOK, gin.H{"redirect_to": redirect})
	}
}

// makeConsentDenyHandler builds POST /oauth2-consent/deny.
type consentDenyBody struct {
	Challenge string `json:"challenge" binding:"required"`
}

func makeConsentDenyHandler(cfg OAuth2FlowHandlerConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg.Hydra == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "hydra_unavailable"})
			return
		}
		var body consentDenyBody
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad_body"})
			return
		}
		redirect, err := cfg.Hydra.RejectConsentRequest(c.Request.Context(), body.Challenge,
			hydraclient.RejectConsentInput{
				Error:            "access_denied",
				ErrorDescription: "The user denied the consent request.",
				StatusCode:       http.StatusForbidden,
			})
		if err != nil {
			cfg.Log.Error("oauth2-consent/deny: RejectConsentRequest failed", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "hydra_reject_failed"})
			return
		}
		cfg.Log.Info("oauth2_consent_denied",
			"event", "hydra_consent_deny",
			"challenge", body.Challenge)
		c.JSON(http.StatusOK, gin.H{"redirect_to": redirect})
	}
}

// computeIDPSessionID derives a stable, non-reversible identifier
// for the current Kratos session so Hydra can link derived OAuth 2
// tokens back to it. The SHA-256 of the cookie value is:
//
//   - stable across retries (deterministic)
//   - non-reversible (cookie itself never hits Hydra's DB — if
//     Hydra's DB is compromised, the attacker can't replay Kratos
//     sessions)
//   - unique enough — SHA-256 collision risk is negligible at any
//     realistic scale
//
// Returns "" when the cookie is missing or zero-length. The caller
// MUST refuse to AcceptLoginRequest in that case; accepting with an
// empty id silently disables the revocation cascade (Decision 5).
func computeIDPSessionID(c *gin.Context) string {
	cookie, err := c.Cookie("ory_kratos_session")
	if err != nil || cookie == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

// buildConsentSession shapes the ConsentSession bag Hydra will mint
// into tokens for this consent. Claims layout per plan §0 Decision 12:
//
//   - id_token: sub (= panel users.id), email, name, jabali.is_admin
//   - access_token: jabali.is_admin (namespaced, for app role mapping)
//
// We deliberately don't populate "name" unless we have first/last
// name fields in panel users — keeping the shape minimal avoids
// leaking PII the OIDC client didn't request.
func buildConsentSession(claims *auth.AccessClaims) hydraclient.ConsentSession {
	if claims == nil {
		return hydraclient.ConsentSession{}
	}
	idTok := map[string]any{
		"email":           claims.Email,
		"jabali.is_admin": claims.IsAdmin,
	}
	accessTok := map[string]any{
		"jabali.is_admin": claims.IsAdmin,
	}
	return hydraclient.ConsentSession{
		IDToken:     idTok,
		AccessToken: accessTok,
	}
}

// userIDFromClaims is a nil-safe helper for log lines — avoid
// panicking on an unset Claims pointer while still producing a
// useful log value.
func userIDFromClaims(c *auth.AccessClaims) string {
	if c == nil {
		return ""
	}
	return c.UserID
}

// isSubset returns true when every element of sub is in super. Uses
// a map to keep the check O(n+m) without pulling in a sort. Case-
// sensitive because OAuth 2 scope strings are.
func isSubset(sub, super []string) bool {
	set := make(map[string]struct{}, len(super))
	for _, s := range super {
		set[s] = struct{}{}
	}
	for _, s := range sub {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

