package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// webmail_vhost.go provides the two agent commands the reconciler uses
// to toggle the per-domain mail.<domain> nginx vhost:
//
//   - webmail.vhost_apply — render + write + nginx reload
//   - webmail.vhost_remove — rm + nginx reload
//
// The vhost proxies browser traffic to Bulwark (127.0.0.1:3000) for the
// webmail UI and to Stalwart's loopback HTTP listener (127.0.0.1:8446)
// for JMAP + admin API calls. The canonical template lives at
// install/nginx/jabali-mail-vhost.conf.tmpl (committed so operators can
// diff it); this file keeps its own copy for build independence. If the
// two drift, the source-of-truth is the committed template — align by
// re-copying here.

// mailVhostTemplate mirrors install/nginx/jabali-mail-vhost.conf.tmpl.
// Go template variables: .Domain, .SSLCertPath, .SSLKeyPath.
const mailVhostTemplate = `# Rendered by panel-agent webmail.vhost_apply (M6 Step 8).
# DO NOT EDIT — changes belong in install/nginx/jabali-mail-vhost.conf.tmpl.

server {
  listen 443 ssl http2;
  server_name mail.{{.DomainName}};

  ssl_certificate {{.SSLCertPath}};
  ssl_certificate_key {{.SSLKeyPath}};

  # Intentionally no X-Forwarded-Proto on this location — Next.js
  # middleware-rewrite uses it to build internal proxy URLs, and with
  # "https" would try to TLS-connect to Bulwark's plain-HTTP upstream.
  # See the source-of-truth template in install/nginx/jabali-mail-vhost.conf.tmpl.
  location / {
    proxy_pass http://127.0.0.1:3000;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
  }

  location /jmap {
    proxy_pass http://127.0.0.1:8446;
    proxy_set_header Host $host;
    proxy_http_version 1.1;
    proxy_read_timeout 3600s;
  }

  location /.well-known/jmap {
    proxy_pass http://127.0.0.1:8446;
    proxy_set_header Host $host;
  }

  location /api {
    proxy_pass http://127.0.0.1:8446;
    proxy_set_header Host $host;
    proxy_http_version 1.1;
  }

  location = /autodiscover/autodiscover.xml { return 404; }
  location = /Autodiscover/Autodiscover.xml { return 404; }
}

server {
  listen 80;
  server_name mail.{{.DomainName}};
  return 301 https://$host$request_uri;
}
`

// mailVhostSitesAvailable + mailVhostSitesEnabled are overridable for
// tests. The naming `<domain>-mail.conf` avoids colliding with the main
// `<domain>.conf` that domain.create writes.
var (
	mailVhostSitesAvailable = "/etc/nginx/sites-available"
	mailVhostSitesEnabled   = "/etc/nginx/sites-enabled"
)

type webmailVhostApplyParams struct {
	DomainName  string `json:"domain_name"`
	SSLCertPath string `json:"ssl_cert_path"`
	SSLKeyPath  string `json:"ssl_key_path"`
}

type webmailVhostResponse struct {
	Ok      bool   `json:"ok"`
	Path    string `json:"path,omitempty"`
	Changed bool   `json:"changed"`
}

func webmailVhostApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p webmailVhostApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if err := validateDomainNameForShell(p.DomainName); err != nil {
		return nil, err
	}
	if p.SSLCertPath == "" || p.SSLKeyPath == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "ssl_cert_path and ssl_key_path are required",
		}
	}

	tmpl, err := template.New("mailvhost").Parse(mailVhostTemplate)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("template parse: %v", err)}
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("template execute: %v", err)}
	}

	configPath := filepath.Join(mailVhostSitesAvailable, p.DomainName+"-mail.conf")
	enabledPath := filepath.Join(mailVhostSitesEnabled, p.DomainName+"-mail.conf")

	// Hash-gated write: if the on-disk content matches what we'd render,
	// skip the write + reload. Matches writeVhost's idempotency contract.
	if existing, err := os.ReadFile(configPath); err == nil && bytes.Equal(existing, rendered.Bytes()) {
		// Ensure the symlink exists even when the content hasn't changed —
		// a prior vhost_remove may have torn it down.
		if _, err := os.Lstat(enabledPath); os.IsNotExist(err) {
			if err := os.Symlink(configPath, enabledPath); err != nil {
				return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("re-link enabled path: %v", err)}
			}
			if err := nginxTestAndReload(ctx); err != nil {
				return nil, err
			}
			return webmailVhostResponse{Ok: true, Path: configPath, Changed: true}, nil
		}
		return webmailVhostResponse{Ok: true, Path: configPath, Changed: false}, nil
	}

	// Atomic write: tmp then rename. If rename fails we leave the tmp file
	// so a subsequent run can diagnose (and the idempotency check above
	// will re-try on next pass).
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, rendered.Bytes(), 0644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("write tmp vhost: %v", err)}
	}
	if err := os.Rename(tmp, configPath); err != nil {
		os.Remove(tmp)
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("rename vhost: %v", err)}
	}

	// (Re-)link into sites-enabled. Re-link unconditionally so a dangling
	// symlink from an earlier broken state is repaired.
	os.Remove(enabledPath)
	if err := os.Symlink(configPath, enabledPath); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("symlink enabled: %v", err)}
	}

	if err := nginxTestAndReload(ctx); err != nil {
		// Roll back the vhost: nginx is currently happy because the bad
		// file isn't loaded yet (we haven't reloaded after the rename).
		// But leaving a bad file in sites-enabled would break the NEXT
		// reload by anyone else. Safer to remove both and surface.
		os.Remove(enabledPath)
		os.Remove(configPath)
		return nil, err
	}
	return webmailVhostResponse{Ok: true, Path: configPath, Changed: true}, nil
}

type webmailVhostRemoveParams struct {
	DomainName string `json:"domain_name"`
}

func webmailVhostRemoveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p webmailVhostRemoveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if err := validateDomainNameForShell(p.DomainName); err != nil {
		return nil, err
	}

	configPath := filepath.Join(mailVhostSitesAvailable, p.DomainName+"-mail.conf")
	enabledPath := filepath.Join(mailVhostSitesEnabled, p.DomainName+"-mail.conf")

	// Idempotent remove. If neither file exists we still return ok+changed=false
	// so the reconciler can cheaply ask us on every tick.
	changed := false
	if _, err := os.Lstat(enabledPath); err == nil {
		if err := os.Remove(enabledPath); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("rm enabled: %v", err)}
		}
		changed = true
	}
	if _, err := os.Stat(configPath); err == nil {
		if err := os.Remove(configPath); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("rm available: %v", err)}
		}
		changed = true
	}

	if changed {
		if err := nginxTestAndReload(ctx); err != nil {
			return nil, err
		}
	}
	return webmailVhostResponse{Ok: true, Changed: changed}, nil
}

// nginxTestAndReload is the tiny shim shared by apply + remove: test
// first, reload only on a clean test. Overridable for tests so unit
// coverage doesn't need a real nginx binary on the host.
var nginxTestAndReload = defaultNginxTestAndReload

func defaultNginxTestAndReload(ctx context.Context) error {
	var out bytes.Buffer
	test := exec.CommandContext(ctx, "nginx", "-t")
	test.Stdout = &out
	test.Stderr = &out
	if err := test.Run(); err != nil {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("nginx test failed: %s", out.String()),
		}
	}
	out.Reset()
	reload := exec.CommandContext(ctx, "systemctl", "reload", "nginx")
	reload.Stdout = &out
	reload.Stderr = &out
	if err := reload.Run(); err != nil {
		return &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("nginx reload failed: %s", out.String()),
		}
	}
	return nil
}

func init() {
	Default.Register("webmail.vhost_apply", webmailVhostApplyHandler)
	Default.Register("webmail.vhost_remove", webmailVhostRemoveHandler)
}
