package clientapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Client is a typed HTTP client for the Jabali Panel API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
}

// NewClient creates a new API client with the given base URL and auth token.
// For localhost with self-signed certs, InsecureSkipVerify is enabled.
func NewClient(baseURL, authToken string) *Client {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		authToken:  authToken,
	}
}

// do performs an HTTP request with the auth token and handles error responses.
func (c *Client) do(ctx context.Context, method, path string, body interface{}, respOut interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	fullURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	// Handle errors
	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error != "" {
			if errResp.Detail != "" {
				return fmt.Errorf("api error: %s (%s)", errResp.Error, errResp.Detail)
			}
			return fmt.Errorf("api error: %s", errResp.Error)
		}
		return fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse success response
	if respOut != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, respOut); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
	}

	return nil
}

// --- Domains ---

// ListDomains fetches all domains (admin sees all, non-admin sees only theirs).
func (c *Client) ListDomains(ctx context.Context, page, pageSize int) (*ListDomainsResponse, error) {
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))
	path := "/domains?" + q.Encode()

	var resp ListDomainsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDomain fetches a single domain by ID.
func (c *Client) GetDomain(ctx context.Context, domainID string) (*DomainResponse, error) {
	path := fmt.Sprintf("/domains/%s", url.PathEscape(domainID))
	var resp DomainResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateDomain creates a new domain.
func (c *Client) CreateDomain(ctx context.Context, req *CreateDomainRequest) (*DomainResponse, error) {
	var resp DomainResponse
	if err := c.do(ctx, http.MethodPost, "/domains", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateDomain updates a domain by ID.
func (c *Client) UpdateDomain(ctx context.Context, domainID string, req *UpdateDomainRequest) (*DomainResponse, error) {
	path := fmt.Sprintf("/domains/%s", url.PathEscape(domainID))
	var resp DomainResponse
	if err := c.do(ctx, http.MethodPatch, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteDomain deletes a domain by ID.
func (c *Client) DeleteDomain(ctx context.Context, domainID string) error {
	path := fmt.Sprintf("/domains/%s", url.PathEscape(domainID))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// --- Users ---

// ListUsers fetches all users (admin only).
func (c *Client) ListUsers(ctx context.Context, page, pageSize int) (*ListUsersResponse, error) {
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))
	path := "/users?" + q.Encode()

	var resp ListUsersResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetUser fetches a single user by ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*UserResponse, error) {
	path := fmt.Sprintf("/users/%s", url.PathEscape(userID))
	var resp UserResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateUser creates a new user.
func (c *Client) CreateUser(ctx context.Context, req *CreateUserRequest) (*UserResponse, error) {
	var resp UserResponse
	if err := c.do(ctx, http.MethodPost, "/users", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateUser updates a user by ID.
func (c *Client) UpdateUser(ctx context.Context, userID string, req *UpdateUserRequest) (*UserResponse, error) {
	path := fmt.Sprintf("/users/%s", url.PathEscape(userID))
	var resp UserResponse
	if err := c.do(ctx, http.MethodPatch, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteUser deletes a user by ID.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	path := fmt.Sprintf("/users/%s", url.PathEscape(userID))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// --- Packages ---

// ListPackages fetches all hosting packages (admin only).
func (c *Client) ListPackages(ctx context.Context, page, pageSize int) (*ListPackagesResponse, error) {
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", page))
	q.Set("page_size", fmt.Sprintf("%d", pageSize))
	path := "/packages?" + q.Encode()

	var resp ListPackagesResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetPackage fetches a single package by ID.
func (c *Client) GetPackage(ctx context.Context, packageID string) (*PackageResponse, error) {
	path := fmt.Sprintf("/packages/%s", url.PathEscape(packageID))
	var resp PackageResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreatePackage creates a new hosting package.
func (c *Client) CreatePackage(ctx context.Context, req *CreatePackageRequest) (*PackageResponse, error) {
	var resp PackageResponse
	if err := c.do(ctx, http.MethodPost, "/packages", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdatePackage updates a package by ID.
func (c *Client) UpdatePackage(ctx context.Context, packageID string, req *UpdatePackageRequest) (*PackageResponse, error) {
	path := fmt.Sprintf("/packages/%s", url.PathEscape(packageID))
	var resp PackageResponse
	if err := c.do(ctx, http.MethodPatch, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeletePackage deletes a package by ID.
func (c *Client) DeletePackage(ctx context.Context, packageID string) error {
	path := fmt.Sprintf("/packages/%s", url.PathEscape(packageID))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
