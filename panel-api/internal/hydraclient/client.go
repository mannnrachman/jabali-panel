// Package hydraclient wraps Ory Hydra's admin API for the panel's use
// cases: minting per-install OAuth 2 clients (apps framework), driving
// the login/consent flow (oauth2_flow handlers), and token
// introspection/revocation.
//
// Every method here takes a context.Context and applies a short (5s)
// per-call deadline layered over the caller's context. Hydra's admin
// API is loopback-only (DSN is MariaDB, requests never leave the
// host), so even a pathological response shouldn't block for longer
// than the network timeout.
//
// Two security invariants the package enforces, straight from
// ADR-0036:
//
//   - **Decision 7 (trusted strip).** CreateClient removes any
//     caller-supplied `metadata.trusted` key before POSTing. A
//     separate server-only method, SetClientTrusted, sets the flag
//     via a round-trip that only applications_service.go should call.
//     A regression here grants silent consent for any scope to any
//     client — the single highest-severity invariant of M16.
//
//   - **Redacted secrets.** CreateClient returns ClientSecret (a
//     string alias whose Stringer returns "[REDACTED]"). Callers that
//     need the bytes (e.g. to AES-GCM-seal for DB persist) call
//     .Raw(); callers that log the struct get the placeholder. Closes
//     the "secret leaked into slog line" regression in plan §3 risks.
package hydraclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultCallTimeout is the per-request ceiling layered on top of the
// caller's ctx. Hydra's admin API is on loopback; 5s covers worst-case
// DB contention during a `hydra migrate sql`-style administrative run
// without making a routine CreateClient feel sluggish.
const defaultCallTimeout = 5 * time.Second

// Client wraps Ory Hydra's admin API. Construct with New and reuse
// across goroutines — the embedded http.Client is safe for concurrent
// use.
type Client struct {
	adminURL string
	http     *http.Client
}

// New returns a Client pointed at Hydra's admin URL (typically
// http://127.0.0.1:4445 in the shipped layout). The URL must NOT be
// proxied externally — see app/hydra_proxy.go for the enforced rule
// that only public endpoints are exposed.
func New(adminURL string) *Client {
	return &Client{
		adminURL: strings.TrimRight(adminURL, "/"),
		http: &http.Client{
			// Per-request timeout is applied via ctx; this is the
			// transport-level ceiling to catch a hung connection
			// before the ctx deadline.
			Timeout: 30 * time.Second,
		},
	}
}

// ClientSecret holds a freshly-minted OAuth 2 client_secret from
// Hydra. The String() method returns "[REDACTED]" so the value is
// safe to pass to slog, fmt.Sprintf, %v formatters. Use .Raw() on the
// single line that actually needs the bytes (AES-GCM seal for DB
// persist).
type ClientSecret string

// String implements fmt.Stringer. Always returns the redaction token
// — the bytes are only exposed via Raw().
func (s ClientSecret) String() string { return "[REDACTED]" }

// Raw returns the underlying secret bytes. Call sites should be
// minimal and every one deliberate — grep for .Raw() periodically to
// audit the leak surface.
func (s ClientSecret) Raw() string { return string(s) }

// CreateClientInput shapes the subset of Hydra's /admin/clients
// payload our callers actually populate. Fields align with
// openid-connect-generic (WordPress) defaults plus what the apps
// framework needs. See plan §0 Decision 7 for the per-install shape.
type CreateClientInput struct {
	// ClientName is shown on the consent screen ("WordPress @
	// example.com/blog").
	ClientName string

	// RedirectURIs must be an exact match at /oauth2/auth time —
	// Hydra rejects anything else. For WP it's the plugin's
	// admin-ajax.php callback URL.
	RedirectURIs []string

	// GrantTypes: M16 enables authorization_code + refresh_token.
	// client_credentials is deliberately off until the Automation
	// API follow-up.
	GrantTypes []string

	// ResponseTypes: always ["code"] for OIDC auth-code flow.
	ResponseTypes []string

	// Scope is a space-separated list. The default for WP SSO is
	// "openid email profile" — every scope we issue must have a
	// ScopeLabel entry in scope_labels.go.
	Scope string

	// TokenEndpointAuthMethod: "client_secret_post" for OIDC plugin
	// compatibility (WP OpenID Connect Generic, Drupal openid_connect,
	// most others). "none" is for public clients (PKCE); we don't
	// use that in M16.
	TokenEndpointAuthMethod string

	// Metadata is an opaque per-client bag. DO NOT set "trusted" here
	// — CreateClient strips it. See package docstring Decision 7.
	//
	// Any key other than "trusted" passes through untouched —
	// applications_service.go stashes the application_install_id
	// here for reverse-lookup on install delete.
	Metadata map[string]any
}

// CreateClientOutput is what CreateClient returns on success. The
// ClientSecret is ONE-SHOT — Hydra doesn't let us read it back later.
// Callers must AES-GCM-seal it into the DB on this exact code path
// or accept that the secret is gone.
type CreateClientOutput struct {
	ClientID     string
	ClientSecret ClientSecret
}

// CreateClient mints a new OAuth 2 / OIDC client in Hydra. See package
// docstring for Decision 7 (metadata.trusted strip) + secret
// redaction invariants.
//
// Errors:
//
//   - ErrConflict: client_id collision (retry with a new id)
//   - ErrUnauthorized: admin URL is misconfigured
//   - *httpError: any other non-2xx from Hydra; includes body
//   - context errors: timeout / cancellation
func (c *Client) CreateClient(ctx context.Context, in CreateClientInput) (CreateClientOutput, error) {
	// Decision 7 — ADR-0036 R1. metadata.trusted grants auto-consent
	// for any scope without user interaction. A regression that lets
	// a caller set it via the Metadata map would be a silent-consent
	// privilege escalation: the caller could register a client, mark
	// it trusted, and harvest tokens for any scope on any redirect
	// with zero user interaction. Strip here; applications_service.go
	// calls SetClientTrusted post-create for panel-managed installs.
	sanitized := sanitizeMetadata(in.Metadata)

	body := map[string]any{
		"client_name":                 in.ClientName,
		"redirect_uris":               in.RedirectURIs,
		"grant_types":                 in.GrantTypes,
		"response_types":              in.ResponseTypes,
		"scope":                       in.Scope,
		"token_endpoint_auth_method":  in.TokenEndpointAuthMethod,
		"metadata":                    sanitized,
		// Force consent to be required unless SetClientTrusted is
		// called later — never inherit whatever Hydra's default is.
		// Redundant with metadata.trusted=false but defense in depth.
		"skip_consent": false,
	}

	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/admin/clients", body, &out); err != nil {
		return CreateClientOutput{}, err
	}
	return CreateClientOutput{
		ClientID:     out.ClientID,
		ClientSecret: ClientSecret(out.ClientSecret),
	}, nil
}

// HydraClient is a read-only view of an OAuth 2 client record, used
// by GetClient + ListClients. Callers that need to mutate should use
// UpdateClient with the full desired shape.
type HydraClient struct {
	ClientID      string         `json:"client_id"`
	ClientName    string         `json:"client_name"`
	RedirectURIs  []string       `json:"redirect_uris"`
	GrantTypes    []string       `json:"grant_types"`
	ResponseTypes []string       `json:"response_types"`
	Scope         string         `json:"scope"`
	Metadata      map[string]any `json:"metadata"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// GetClient fetches an OAuth 2 client by ID. Returns ErrNotFound if
// the client doesn't exist in Hydra (including the harmless
// "already deleted" case).
func (c *Client) GetClient(ctx context.Context, clientID string) (HydraClient, error) {
	var out HydraClient
	path := "/admin/clients/" + url.PathEscape(clientID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return HydraClient{}, err
	}
	return out, nil
}

// DeleteClient removes an OAuth 2 client from Hydra. Idempotent:
// ErrNotFound is returned but callers are expected to treat it as
// success (the apps framework's compensating-transaction on install
// delete does exactly that — an orphan client is harmless, so a
// 404 on delete means someone got there first).
func (c *Client) DeleteClient(ctx context.Context, clientID string) error {
	path := "/admin/clients/" + url.PathEscape(clientID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// SetClientTrusted marks an OAuth 2 client as trusted (auto-consent)
// in Hydra. MUST ONLY be called from applications_service.go for
// panel-managed installs — there is no "user-facing" path to this
// method, and there must not be one. A grep for SetClientTrusted
// should return exactly one callsite: the trusted-client provisioning
// in the applications install flow.
//
// Implementation: GET the existing client, merge metadata.trusted =
// trusted, PUT back. The RFC 6902 JSON Patch endpoint would be
// cleaner, but using GET+PUT keeps us independent of Hydra's
// per-version patch semantics.
func (c *Client) SetClientTrusted(ctx context.Context, clientID string, trusted bool) error {
	existing, err := c.GetClient(ctx, clientID)
	if err != nil {
		return fmt.Errorf("fetch client for trust update: %w", err)
	}

	md := existing.Metadata
	if md == nil {
		md = map[string]any{}
	}
	md["trusted"] = trusted

	// Hydra's PUT /admin/clients/{id} replaces the whole record, so
	// we have to echo every field back. Extract the fields we care
	// about; let Hydra default the rest.
	body := map[string]any{
		"client_id":      existing.ClientID,
		"client_name":    existing.ClientName,
		"redirect_uris":  existing.RedirectURIs,
		"grant_types":    existing.GrantTypes,
		"response_types": existing.ResponseTypes,
		"scope":          existing.Scope,
		"metadata":       md,
		// skip_consent mirrors metadata.trusted. Hydra honors either
		// when deciding to skip the consent UI; set both so the
		// runtime behavior is unambiguous.
		"skip_consent": trusted,
	}
	path := "/admin/clients/" + url.PathEscape(clientID)
	return c.doJSON(ctx, http.MethodPut, path, body, nil)
}

// sanitizeMetadata returns a copy of m with the reserved "trusted"
// key removed. Exposed as a package-private helper so the Decision 7
// strip is testable in isolation.
func sanitizeMetadata(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		// Reserved per Decision 7 — server-side only via
		// SetClientTrusted. ANY caller-supplied value for this key
		// is dropped; do not "pass through if false" because that
		// would re-introduce the strip-on-send / fill-on-receive
		// asymmetry Decision 7 is trying to prevent.
		if k == "trusted" {
			continue
		}
		out[k] = v
	}
	return out
}

// doJSON issues a JSON request against Hydra's admin URL. Handles
// timeouts, request marshaling, status-code-to-typed-error mapping,
// and response unmarshaling into out (or discard when out==nil).
func (c *Client) doJSON(ctx context.Context, method, path string, reqBody, out any) error {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	var bodyReader io.Reader
	if reqBody != nil {
		buf, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.adminURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hydra admin %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if err := mapStatusErr(resp.StatusCode, string(bodyBytes)); err != nil {
		return err
	}
	if out == nil || len(bodyBytes) == 0 {
		return nil
	}
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
