package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/certbot"
)

// sslIssueParams is the input shape for ssl.issue.
//
// Hostnames lists extra SANs beyond the primary Domain — used by M6.1
// to include mail.<domain> + autoconfig.<domain> when email is enabled.
// Empty slice = single-SAN cert (legacy behavior). Each hostname is
// validated against sslDomainRegex.
type sslIssueParams struct {
	Domain    string   `json:"domain"`
	Webroot   string   `json:"webroot"`
	Email     string   `json:"email"`
	Staging   bool     `json:"staging"`
	Hostnames []string `json:"hostnames,omitempty"`
}

// sslIssueResponse is the output shape for ssl.issue.
type sslIssueResponse struct {
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
	Staging   bool   `json:"staging"`
}

// sslDomainRegex for validation: must be a valid FQDN (allows uppercase and mixed case for SSL certs).
var sslDomainRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{1,253}$`)

// emailRegex for basic email validation.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

func sslIssueHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sslIssueParams
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

	// Validate webroot is absolute and under allowed prefixes
	if !isAllowedWebroot(p.Webroot) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid webroot %q: must be absolute path under /home/ or /var/www/", p.Webroot),
		}
	}

	// Validate email format
	if !emailRegex.MatchString(p.Email) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid email %q: must be a valid email address", p.Email),
		}
	}

	// Validate each extra hostname with the same regex as the primary domain.
	for _, h := range p.Hostnames {
		if !sslDomainRegex.MatchString(h) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("invalid hostname %q in hostnames[]", h),
			}
		}
	}

	// Run certbot
	runner := certbot.NewRunner()
	result, err := runner.Issue(p.Domain, p.Webroot, p.Email, p.Staging, p.Hostnames)

	if err != nil {
		details, _ := json.Marshal(map[string]any{
			"reason": result.Reason,
			"stderr": result.Stderr,
		})
		actionable := certbot.ExtractActionableDetail(result.Stderr)
		msg := fmt.Sprintf("certbot issue failed: %v", err)
		if actionable != "" {
			msg = fmt.Sprintf("certbot issue failed (%s): %s", result.Reason, actionable)
		}
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: msg,
			Details: json.RawMessage(details),
		}
	}

	return sslIssueResponse{
		CertPath:  result.CertPath,
		KeyPath:   result.KeyPath,
		IssuedAt:  result.IssuedAt.UTC().Format("2006-01-02T15:04:05Z"),
		ExpiresAt: result.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		Staging:   p.Staging,
	}, nil
}

// isAllowedWebroot validates webroot is absolute and under safe prefixes.
func isAllowedWebroot(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	if strings.HasPrefix(path, "/home/") || strings.HasPrefix(path, "/var/www/") {
		return true
	}
	return false
}

func init() {
	Default.Register("ssl.issue", sslIssueHandler)
}
