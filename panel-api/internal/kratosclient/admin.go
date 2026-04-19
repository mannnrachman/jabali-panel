package kratosclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// AdminTraits is the identity-trait payload Kratos stores under `identity.traits`.
// Jabali's identity schema (install/kratos-identity-schema.json) defines these
// three fields; the IsAdmin flag is boolean so `omitempty` would drop the false
// value and break the schema's required-fields check — tag is plain `json:"is_admin"`.
type AdminTraits struct {
	Email    string `json:"email"`
	Username string `json:"username,omitempty"`
	IsAdmin  bool   `json:"is_admin"`
}

// AdminIdentity is the subset of the Kratos admin-API identity shape that
// Jabali consumes. The full shape has more fields (metadata_public,
// verifiable_addresses, …) but we only read id + traits today.
type AdminIdentity struct {
	ID     string      `json:"id"`
	Traits AdminTraits `json:"traits"`
}

// CreateIdentityWithPassword posts to POST {admin}/admin/identities with a
// bcrypt-hashed password under credentials.password.config.hashed_password.
// Kratos 1.3.1 accepts $2a$/$2b$/$2y$ hashes verbatim when the config enables
// hashers.bcrypt (see install/kratos.yml.tmpl). Returns the new identity ID.
//
// passwordHash MUST be a full bcrypt digest (60 chars, starts with $2a$, $2b$,
// or $2y$). Passing a plain password or a truncated hash is caught by Kratos
// with a 400 — but we refuse empty/obviously-too-short hashes up front so the
// operator sees a local-level error with context.
func (c *Client) CreateIdentityWithPassword(ctx context.Context, traits AdminTraits, passwordHash string) (string, error) {
	if len(passwordHash) < 59 {
		return "", fmt.Errorf("createidentitywithpassword: bcrypt hash looks invalid (got %d chars, want 60)", len(passwordHash))
	}

	payload := map[string]any{
		"schema_id": "default",
		"traits":    traits,
		"credentials": map[string]any{
			"password": map[string]any{
				"config": map[string]any{
					"hashed_password": passwordHash,
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("createidentitywithpassword: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.adminURL+"/admin/identities", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("createidentitywithpassword: request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("createidentitywithpassword: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		errBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("createidentitywithpassword: status %d: %s", resp.StatusCode, string(errBody))
	}

	var result AdminIdentity
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("createidentitywithpassword: decode: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("createidentitywithpassword: kratos returned empty id")
	}

	return result.ID, nil
}

// DeleteIdentity removes an identity by ID. 404 is treated as success because
// a missing identity is the desired end state; this keeps the delete idempotent
// during unwind/cleanup paths where we may race with a concurrent delete.
// Returns nil for an empty ID (callers pass the zero value when a prior create
// failed; the defer would otherwise hit /admin/identities/ and confuse the log).
func (c *Client) DeleteIdentity(ctx context.Context, identityID string) error {
	if identityID == "" {
		return nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.adminURL+"/admin/identities/"+url.PathEscape(identityID), nil)
	if err != nil {
		return fmt.Errorf("deleteidentity: request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("deleteidentity: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deleteidentity: status %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}

// ListIdentitiesPage fetches up to perPage identities starting at pageToken.
// Caller iterates until the returned next page token is empty.
//
// Kratos 1.3.1 admin-API pagination is keyset-based: the response includes a
// `Link: <...>; rel="next"` header whose URL exposes the next `page_token`.
// For simplicity we parse just the token out of that URL; if the header is
// missing or rel="next" isn't present, nextPageToken is returned as "".
func (c *Client) ListIdentitiesPage(ctx context.Context, perPage int, pageToken string) ([]AdminIdentity, string, error) {
	if perPage <= 0 {
		perPage = 250
	}

	q := url.Values{}
	q.Set("per_page", strconv.Itoa(perPage))
	if pageToken != "" {
		q.Set("page_token", pageToken)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.adminURL+"/admin/identities?"+q.Encode(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("listidentities: request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("listidentities: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("listidentities: status %d: %s", resp.StatusCode, string(errBody))
	}

	var ids []AdminIdentity
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		return nil, "", fmt.Errorf("listidentities: decode: %w", err)
	}

	next := parseNextPageToken(resp.Header.Get("Link"))
	return ids, next, nil
}

// AllIdentitiesByEmail scans every admin-identities page once and returns a map
// from lowercased email → identity ID. Used for migration idempotency: if an
// email already maps to a Kratos identity, kratos-migrate skips it instead of
// retrying CreateIdentityWithPassword (which would 409 on duplicate traits).
//
// Callers should accept that the map reflects the state at scan time; new
// identities created concurrently won't show up. For a single-operator panel
// this is acceptable.
func (c *Client) AllIdentitiesByEmail(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	token := ""
	for {
		page, next, err := c.ListIdentitiesPage(ctx, 250, token)
		if err != nil {
			return nil, err
		}
		for _, id := range page {
			if id.Traits.Email == "" {
				continue
			}
			out[lc(id.Traits.Email)] = id.ID
		}
		if next == "" {
			break
		}
		token = next
	}
	return out, nil
}

// parseNextPageToken extracts the `page_token` query param from the Link
// header's rel="next" URL, if present. Returns "" when there's no next page.
func parseNextPageToken(linkHeader string) string {
	// Simple parser: look for <URL>; rel="next" and pull page_token from URL.
	// Kratos format: `<https://.../admin/identities?per_page=250&page_token=ABC>; rel="next"`.
	if linkHeader == "" {
		return ""
	}
	for _, part := range splitCSV(linkHeader) {
		if !containsRel(part, "next") {
			continue
		}
		u := extractURL(part)
		if u == "" {
			continue
		}
		parsed, err := url.Parse(u)
		if err != nil {
			continue
		}
		return parsed.Query().Get("page_token")
	}
	return ""
}

func splitCSV(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func containsRel(segment, want string) bool {
	// segment like: `<url>; rel="next"` — cheap substring match is fine.
	return bytes.Contains([]byte(segment), []byte(`rel="`+want+`"`))
}

func extractURL(segment string) string {
	start := bytes.IndexByte([]byte(segment), '<')
	end := bytes.IndexByte([]byte(segment), '>')
	if start < 0 || end < 0 || end <= start+1 {
		return ""
	}
	return segment[start+1 : end]
}

func lc(s string) string {
	// ASCII-only lowercase is fine for email local-parts + domains.
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// randomCanarySuffix returns a hex-encoded random token for constructing a
// disposable email + unique trait values during the bcrypt canary check.
func randomCanarySuffix() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
