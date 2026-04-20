package hydraclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// IntrospectResult is Hydra's /admin/oauth2/introspect response. Used
// by the (future) Automation API middleware to validate bearer tokens
// and their scopes, and by the runbook's "is this token still valid"
// operator check. Active=false short-circuits everything else —
// Hydra returns {"active": false} for revoked/expired tokens.
type IntrospectResult struct {
	Active    bool     `json:"active"`
	Scope     string   `json:"scope"`
	ClientID  string   `json:"client_id"`
	Subject   string   `json:"sub"`
	ExpiresAt int64    `json:"exp"`
	IssuedAt  int64    `json:"iat"`
	Audience  []string `json:"aud"`
	TokenType string   `json:"token_type"`

	// Ext carries the custom claims from the access token
	// (plan §0 Decision 12: jabali.is_admin). Populated by Hydra
	// from AcceptConsentRequest's ConsentSession.AccessToken.
	Ext map[string]any `json:"ext,omitempty"`
}

// IntrospectToken queries Hydra for the live status of an access or
// refresh token. The admin-API endpoint is POST with a form-encoded
// body (unlike the rest of our admin calls which are JSON), matching
// RFC 7662.
//
// scope is optional; if set, Hydra only returns active=true when the
// token was issued with AT LEAST that scope. Leave empty to skip the
// scope check.
func (c *Client) IntrospectToken(ctx context.Context, token, scope string) (IntrospectResult, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("token", token)
	if scope != "" {
		form.Set("scope", scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.adminURL+"/admin/oauth2/introspect",
		strings.NewReader(form.Encode()))
	if err != nil {
		return IntrospectResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return IntrospectResult{}, fmt.Errorf("hydra introspect: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if err := mapStatusErr(resp.StatusCode, string(bodyBytes)); err != nil {
		return IntrospectResult{}, err
	}
	var out IntrospectResult
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return IntrospectResult{}, fmt.Errorf("decode introspect: %w", err)
	}
	return out, nil
}

// RevokeSessionsByIdentityProviderSessionID invalidates every Hydra
// access+refresh token whose originating login was accepted with
// `identity_provider_session_id = idpSessionID`. This is the
// Decision 5 cascade point: when Kratos revokes a session, we
// compute SHA-256(cookie) and call this method to kill derived
// tokens across every OIDC client.
//
// Wired into the Kratos-revocation hook in a follow-up step (Wave B
// doesn't create the hook itself; it only lands the client-side
// endpoint so the hook has something to call).
func (c *Client) RevokeSessionsByIdentityProviderSessionID(ctx context.Context, idpSessionID string) error {
	q := url.Values{}
	q.Set("sid", idpSessionID)
	path := "/admin/oauth2/auth/sessions/logout?" + q.Encode()
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}
