package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/certbot"
)

// sslRenewParams is the input shape for ssl.renew.
type sslRenewParams struct {
	Domain string `json:"domain"`
	Force  bool   `json:"force"`
}

// sslRenewResponse is the output shape for ssl.renew.
type sslRenewResponse struct {
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
	Skipped   bool   `json:"skipped"`
}

func sslRenewHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sslRenewParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate domain format
	if !sslDomainRegex.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid domain %q: must match ^[a-zA-Z0-9][a-zA-Z0-9.-]{1,253}$", p.Domain),
		}
	}

	// Run certbot renew
	runner := certbot.NewRunner()
	result, err := runner.Renew(p.Domain, p.Force)

	if err != nil {
		details, _ := json.Marshal(map[string]any{
			"reason":  result.Reason,
			"stderr":  result.Stderr,
			"skipped": result.Skipped,
		})
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("certbot renew failed: %v", err),
			Details: json.RawMessage(details),
		}
	}

	return sslRenewResponse{
		CertPath:  result.CertPath,
		KeyPath:   result.KeyPath,
		IssuedAt:  result.IssuedAt.UTC().Format("2006-01-02T15:04:05Z"),
		ExpiresAt: result.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		Skipped:   result.Skipped,
	}, nil
}

func init() {
	Default.Register("ssl.renew", sslRenewHandler)
}
