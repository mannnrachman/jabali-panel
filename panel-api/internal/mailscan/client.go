// Package mailscan implements the M33.2 async mail YARA scanner. It
// runs as an in-process reconciler tick inside panel-api, walks Stalwart
// mailboxes via JMAP, scans attachments with yara-x (`yr`), moves hits to
// a per-account "Malware" mailbox, and emits malware_quarantine_added
// ingest events (source=yara). ADR-0079.
//
// JMAP client below is a focused copy of panel-agent/internal/commands/
// mailbox_jmap.go — kept separate during M33.2 to avoid rebase friction
// against in-flight M30/M33 branches. Both targets Stalwart 0.16; drift
// is caught by the live-VM smoke tests. TODO(post-M33.2): consolidate
// into a top-level internal/jmap package when both branches land.
package mailscan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultStalwartAdminURL  = "http://127.0.0.1:8446"
	defaultAdminTokenPath    = "/etc/jabali-panel/stalwart-admin.token"
	jmapAPIPath              = "/jmap/"
	jmapSessionPath          = "/jmap/session"
	stalwartHTTPTimeout      = 30 * time.Second
	jmapAdminUser            = "admin"
	envStalwartAdminURL      = "JABALI_STALWART_ADMIN_URL"
	envStalwartAdminTokenPath = "JABALI_STALWART_ADMIN_TOKEN_PATH"
)

// jmapUsing is the capability set the scanner advertises. principals is
// required for Principal/query (multi-tenant enumeration). mail covers
// Mailbox/Email/Blob ops.
var jmapUsing = []string{
	"urn:ietf:params:jmap:core",
	"urn:ietf:params:jmap:mail",
	"urn:ietf:params:jmap:principals",
	"urn:ietf:params:jmap:blob",
}

// Client wraps an authenticated session against Stalwart's JMAP endpoint.
// Created once per tick to capture a fresh sessionState and downloadUrl
// template (avoids stale-blob-id 404 across long-running processes).
type Client struct {
	baseURL   string
	authValue string
	http      *http.Client
	// session metadata pulled from /jmap/session.
	apiURL      string
	downloadTpl string
	username    string
}

// NewClient builds an authenticated client by reading the stalwart admin
// token from disk and dialling /jmap/session. Returns an error if the
// token file is missing, the session call fails, or the response is
// missing required fields. Token reads are cheap (panel-api process is
// the file owner — 0640 jabali:jabali-mail).
func NewClient(ctx context.Context) (*Client, error) {
	baseURL := envOr(envStalwartAdminURL, defaultStalwartAdminURL)
	tokenPath := envOr(envStalwartAdminTokenPath, defaultAdminTokenPath)
	tokenBytes, err := os.ReadFile(tokenPath) //nolint:gosec // operator-owned
	if err != nil {
		return nil, fmt.Errorf("read stalwart admin token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("stalwart admin token at %s is empty", tokenPath)
	}
	c := &Client{
		baseURL:   baseURL,
		authValue: basicAuth(jmapAdminUser, token),
		http:      &http.Client{Timeout: stalwartHTTPTimeout},
	}
	if err := c.discover(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func basicAuth(u, p string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(u+":"+p))
}

// jmapSession is the subset of /jmap/session we care about.
type jmapSession struct {
	Username    string `json:"username"`
	APIURL      string `json:"apiUrl"`
	DownloadURL string `json:"downloadUrl"`
}

func (c *Client) discover(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+jmapSessionPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authValue)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET /jmap/session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET /jmap/session: status %d", resp.StatusCode)
	}
	var s jmapSession
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return fmt.Errorf("decode /jmap/session: %w", err)
	}
	if s.APIURL == "" {
		return fmt.Errorf("/jmap/session missing apiUrl")
	}
	c.apiURL = rewriteHostToBase(s.APIURL, c.baseURL)
	c.downloadTpl = rewriteHostToBase(s.DownloadURL, c.baseURL)
	c.username = s.Username
	return nil
}

// rewriteHostToBase replaces the host in `u` with the host of `base`.
// Stalwart returns its configured FQDN (e.g. mx.jabali-panel.local) in
// session URLs but we always dial 127.0.0.1:8446. Without the rewrite,
// downloads would fail when the FQDN isn't resolvable inside the panel.
func rewriteHostToBase(u, base string) string {
	if u == "" || base == "" {
		return u
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	baseParsed, err := url.Parse(base)
	if err != nil {
		return u
	}
	parsed.Scheme = baseParsed.Scheme
	parsed.Host = baseParsed.Host
	return parsed.String()
}

// ---- JMAP request/response wire ----

type jmapRequest struct {
	Using       []string         `json:"using"`
	MethodCalls []jmapMethodCall `json:"methodCalls"`
}

// jmapMethodCall is a [name, args, callId] triple per RFC 8620 §3.3.
type jmapMethodCall struct {
	Name   string
	Args   any
	CallID string
}

func (c jmapMethodCall) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{c.Name, c.Args, c.CallID})
}

type jmapResponse struct {
	MethodResponses []jmapMethodResponse `json:"methodResponses"`
	SessionState    string               `json:"sessionState"`
}

type jmapMethodResponse struct {
	Name   string
	Args   json.RawMessage
	CallID string
}

func (r *jmapMethodResponse) UnmarshalJSON(data []byte) error {
	var arr [3]json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("method response must be a 3-element array: %w", err)
	}
	if err := json.Unmarshal(arr[0], &r.Name); err != nil {
		return err
	}
	r.Args = arr[1]
	return json.Unmarshal(arr[2], &r.CallID)
}

// call executes one or more JMAP method calls in a single request. The
// response array is positional — calls[i] returns responses[i] (or an
// error response with name "error"). Single-call helpers below wrap this.
func (c *Client) call(ctx context.Context, calls ...jmapMethodCall) (*jmapResponse, error) {
	body, err := json.Marshal(jmapRequest{Using: jmapUsing, MethodCalls: calls})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authValue)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", c.apiURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("JMAP %d: %s", resp.StatusCode, string(b))
	}
	var out jmapResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode JMAP response: %w", err)
	}
	return &out, nil
}

// callSingle runs one method call and unmarshals its args into `dst`.
// Returns an error if the response is a JMAP `error` envelope or the
// callId doesn't match (defence-in-depth against framing bugs).
func (c *Client) callSingle(ctx context.Context, name string, args any, dst any) error {
	resp, err := c.call(ctx, jmapMethodCall{Name: name, Args: args, CallID: "c1"})
	if err != nil {
		return err
	}
	if len(resp.MethodResponses) == 0 {
		return fmt.Errorf("JMAP empty methodResponses for %s", name)
	}
	first := resp.MethodResponses[0]
	if first.Name == "error" {
		return fmt.Errorf("JMAP error from %s: %s", name, string(first.Args))
	}
	if first.Name != name {
		return fmt.Errorf("JMAP response name mismatch: want %s got %s", name, first.Name)
	}
	if dst != nil {
		if err := json.Unmarshal(first.Args, dst); err != nil {
			return fmt.Errorf("decode %s args: %w", name, err)
		}
	}
	return nil
}

// downloadBlob fetches an attachment by accountId + blobId. Returns the
// body capped at maxBytes — anything larger is truncated and the caller
// treats it as un-scannable (yr would just OOM). Stalwart's downloadUrl
// template uses {accountId} {blobId} {name} {type} placeholders.
func (c *Client) downloadBlob(ctx context.Context, accountID, blobID, name, mimeType string, maxBytes int64) ([]byte, bool, error) {
	if c.downloadTpl == "" {
		return nil, false, fmt.Errorf("downloadUrl template missing — session.discover did not populate it")
	}
	rawURL := c.downloadTpl
	rawURL = strings.ReplaceAll(rawURL, "{accountId}", url.PathEscape(accountID))
	rawURL = strings.ReplaceAll(rawURL, "{blobId}", url.PathEscape(blobID))
	rawURL = strings.ReplaceAll(rawURL, "{name}", url.PathEscape(name))
	rawURL = strings.ReplaceAll(rawURL, "{type}", url.QueryEscape(mimeType))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", c.authValue)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("blob download %d for %s/%s", resp.StatusCode, accountID, blobID)
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, fmt.Errorf("read blob body: %w", err)
	}
	truncated := int64(len(buf)) > maxBytes
	if truncated {
		buf = buf[:maxBytes]
	}
	return buf, truncated, nil
}

// envOr is the only env-var helper the package needs (Client + tick read
// the same overrides). Kept private to avoid leaking globals.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

