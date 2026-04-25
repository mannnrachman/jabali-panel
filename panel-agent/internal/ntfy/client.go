// Package ntfy is a minimal client for ntfy.sh-compatible push servers.
// We use the Jabali team's https://ntfy.jabali-panel.com instance to
// receive diagnostic-report links from operators who hit the Send button
// on /jabali-admin/support.
//
// Protocol: POST plaintext body to <baseURL>/<topic>. Headers Title +
// Priority are optional. Response 200 means delivered.
package ntfy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Publish posts the body to <baseURL>/<topic>. title and priority are
// optional; pass empty / 0 to skip. Returns nil on 200/202.
func (c *Client) Publish(ctx context.Context, topic, title, body string, priority int) error {
	url := c.BaseURL + "/" + strings.TrimLeft(topic, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if title != "" {
		req.Header.Set("Title", title)
	}
	if priority > 0 {
		req.Header.Set("Priority", fmt.Sprintf("%d", priority))
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy %d: %s", resp.StatusCode, string(bodyBytes))
	}
	return nil
}
