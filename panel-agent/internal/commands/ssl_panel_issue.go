package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/certbot"
)

// sslPanelIssueParams is the input shape for ssl.panel.issue.
//
// Hostname is the panel's canonical FQDN (server_settings.hostname);
// ExtraHostnames typically carries `mail.<hostname>` so Bulwark's
// vhost gets the same cert via SAN. Email is the LE registration
// address (server_settings.admin_email). Staging gates --staging on
// the certbot invocation so admins can rehearse without burning the
// 50-cert/week LE rate limit.
type sslPanelIssueParams struct {
	Hostname       string   `json:"hostname"`
	ExtraHostnames []string `json:"extra_hostnames,omitempty"`
	Email          string   `json:"email"`
	Staging        bool     `json:"staging,omitempty"`
}

// sslPanelIssueResponse mirrors the agent's ssl.issue shape so callers
// can treat both response types uniformly.
type sslPanelIssueResponse struct {
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
	Staging   bool   `json:"staging"`
}

// panelACMEWebroot is the directory nginx serves /.well-known/acme-challenge/
// from for the panel hostname's HTTP-01 challenges. install.sh's
// install_jabali_panel_cert_hook step creates this with mode 0750
// owned by root:www-data so certbot (root) writes and nginx
// (www-data) reads.
const panelACMEWebroot = "/var/www/jabali-panel-acme"

// panelDeployHook is the certbot deploy-hook script install.sh
// drops at /etc/letsencrypt/renewal-hooks/deploy/. The agent
// invokes it directly after a first-issue so the cert lands at
// /etc/jabali/tls/panel.{crt,key} and the consuming services
// (nginx, jabali-panel, jabali-bulwark) reload — same path certbot
// renewals will run through unattended.
const panelDeployHook = "/etc/letsencrypt/renewal-hooks/deploy/jabali-panel-cert.sh"

// runDeployHookFn is a package-level seam so unit tests can stub
// out the actual exec.Command without spawning processes. Production
// wiring is exec.Command + cmd.Run().
var runDeployHookFn = func(ctx context.Context, hostname string) error {
	cmd := exec.CommandContext(ctx, panelDeployHook)
	// certbot sets RENEWED_LINEAGE on real renewals; for first-issue
	// we replicate the contract so the hook script doesn't have to
	// special-case its caller.
	cmd.Env = append(os.Environ(),
		"RENEWED_LINEAGE=/etc/letsencrypt/live/"+hostname,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("deploy-hook failed: %v: %s", err, string(out))
	}
	return nil
}

func sslPanelIssueHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sslPanelIssueParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	if !sslDomainRegex.MatchString(p.Hostname) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid hostname %q", p.Hostname),
		}
	}
	for _, h := range p.ExtraHostnames {
		if !sslDomainRegex.MatchString(h) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("invalid hostname %q in extra_hostnames[]", h),
			}
		}
	}
	if !emailRegex.MatchString(p.Email) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid email %q", p.Email),
		}
	}

	// The panel-acme webroot must exist with the right perms before
	// certbot writes its challenge file. install.sh provisions this;
	// we tolerate a missing directory gracefully (return a clear
	// error) rather than chmod-ing on the agent's behalf — that's an
	// install-time concern, not a runtime one.
	if info, err := os.Stat(panelACMEWebroot); err != nil || !info.IsDir() {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("panel acme webroot missing at %s — re-run install.sh", panelACMEWebroot),
		}
	}

	runner := certbot.NewRunner()
	result, err := runner.Issue(p.Hostname, panelACMEWebroot, p.Email, p.Staging, p.ExtraHostnames)
	if err != nil {
		details, _ := json.Marshal(map[string]any{
			"reason": result.Reason,
			"stderr": result.Stderr,
		})
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("certbot panel issue failed: %v", err),
			Details: json.RawMessage(details),
		}
	}

	// Run the deploy-hook directly. Certbot only fires deploy-hooks
	// on renewals, NOT on first-issue, so the agent triggers it once
	// the cert lineage exists.
	if err := runDeployHookFn(ctx, p.Hostname); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}

	return sslPanelIssueResponse{
		CertPath:  result.CertPath,
		KeyPath:   result.KeyPath,
		IssuedAt:  result.IssuedAt.UTC().Format("2006-01-02T15:04:05Z"),
		ExpiresAt: result.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		Staging:   p.Staging,
	}, nil
}

func init() {
	Default.Register("ssl.panel.issue", sslPanelIssueHandler)
}
