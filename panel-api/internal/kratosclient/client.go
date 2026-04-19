// Package kratosclient provides a minimal HTTP client for Ory Kratos identity platform.
// It handles session validation via the /sessions/whoami endpoint and caches results
// to reduce upstream load during high-concurrency periods.
package kratosclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUnauthenticated = errors.New("unauthenticated")

// Identity is the session identity response from Kratos /sessions/whoami.
// It captures the minimal fields needed for Jabali's RBAC (email, username, is_admin).
type Identity struct {
	ID     string                 `json:"id"`
	Traits map[string]interface{} `json:"traits"`
}

// Client is a Kratos session validator. It calls /sessions/whoami and caches results
// by cookie hash to reduce upstream load.
type Client struct {
	publicURL  string
	adminURL   string
	httpClient *http.Client
	cache      *Cache
}

// NewClient returns a Kratos client targeting the given public and admin URLs.
// publicURL and adminURL should be complete base URLs (e.g., "http://localhost:4433").
func NewClient(publicURL, adminURL string) *Client {
	return &Client{
		publicURL:  strings.TrimSuffix(publicURL, "/"),
		adminURL:   strings.TrimSuffix(adminURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
		cache:      NewCache(10000, 10*time.Second),
	}
}

// Whoami validates a Kratos session cookie and returns the authenticated identity.
// The cookie is expected to be the raw cookie value (not the "ory_kratos_session=" prefix).
// Results are cached by cookie hash; cache misses round-trip to the Kratos public endpoint.
func (c *Client) Whoami(ctx context.Context, cookie string) (*Identity, error) {
	if cookie == "" {
		return nil, ErrUnauthenticated
	}

	// Check cache first.
	if identity, ok := c.cache.Get(cookie); ok {
		return identity, nil
	}

	// Cache miss: validate via /sessions/whoami.
	identity, err := c.whoamiRemote(ctx, cookie)
	if err != nil {
		return nil, err
	}

	// Cache the result for future lookups.
	c.cache.Set(cookie, identity)

	return identity, nil
}

// whoamiRemote calls the Kratos public /sessions/whoami endpoint with the session cookie.
func (c *Client) whoamiRemote(ctx context.Context, cookie string) (*Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.publicURL+"/sessions/whoami", nil)
	if err != nil {
		return nil, fmt.Errorf("whoami: failed to create request: %w", err)
	}

	// Attach the session cookie.
	req.AddCookie(&http.Cookie{
		Name:  "ory_kratos_session",
		Value: cookie,
	})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("whoami: request failed: %w", err)
	}
	defer resp.Body.Close()

	// 200 OK = authenticated; 401 = unauthenticated.
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthenticated
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("whoami: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var identity Identity
	if err := json.NewDecoder(resp.Body).Decode(&identity); err != nil {
		return nil, fmt.Errorf("whoami: failed to decode response: %w", err)
	}

	return &identity, nil
}

// GetTraitEmail extracts the email from a Kratos identity's traits.
// Returns empty string if not found.
func (i *Identity) GetTraitEmail() string {
	if email, ok := i.Traits["email"].(string); ok {
		return email
	}
	return ""
}

// GetTraitUsername extracts the username from a Kratos identity's traits.
// Returns empty string if not found.
func (i *Identity) GetTraitUsername() string {
	if username, ok := i.Traits["username"].(string); ok {
		return username
	}
	return ""
}

// GetTraitIsAdmin extracts the is_admin flag from a Kratos identity's traits.
// Returns false if not found or not a boolean.
func (i *Identity) GetTraitIsAdmin() bool {
	// is_admin might be a bool or a string "true"/"false" depending on how it was set.
	switch v := i.Traits["is_admin"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}
