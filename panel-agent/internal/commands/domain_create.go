package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainCreateParams is the input shape for domain.create.
type domainCreateParams struct {
	Username           string `json:"username"`
	Domain             string `json:"domain"`
	DocRoot            string `json:"doc_root"`
	PHPVersion         string `json:"php_version"`
	CustomDirectives   string `json:"custom_directives"`
	// IsEnabled controls whether the vhost serves the tenant's docroot
	// (true) or a branded "site disabled" placeholder (false). Pointer
	// so omitted fields default to true (backwards compat).
	IsEnabled *bool `json:"is_enabled"`
}

// domainCreateResponse is the output shape for domain.create.
type domainCreateResponse struct {
	Domain     string `json:"domain"`
	DocRoot    string `json:"doc_root"`
	ConfigPath string `json:"config_path"`
}

// domainRegex validates domain name format (lowercase hostname).
// Pattern: ^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$
var domainRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// vhostTemplate is the nginx vhost configuration template.
const vhostTemplate = `server {
    listen 80;
    listen [::]:80;
    server_name {{.Domain}};
{{ if .IsEnabled }}
    root {{.DocRoot}};
    index index.html index.php;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.Username}}.sock;
        fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
        include fastcgi_params;
    }

    access_log /var/log/nginx/{{.Domain}}-access.log;
    error_log /var/log/nginx/{{.Domain}}-error.log;

    {{.CustomDirectives}}
{{ else }}
    # Domain is administratively disabled. Serve the branded
    # disabled page instead of the tenant's docroot. Keep access
    # logs under the normal filename so ops can still see hits.
    root /var/www/jabali-disabled;
    index index.html;
    access_log /var/log/nginx/{{.Domain}}-access.log;
    error_log /var/log/nginx/{{.Domain}}-error.log;
    location / { try_files /index.html =503; }
{{ end }}
}
`

type vhostData struct {
	Domain           string
	DocRoot          string
	PHPVersion       string
	Username         string
	CustomDirectives string
	IsEnabled        bool
}

// pathsUnderHome returns each directory from docRoot up to but NOT
// including /home/<user>, ordered from deepest to shallowest. Used to
// chown every intermediate directory the panel creates so tenant data
// never gets stranded under root ownership.
//
// Returns nil if docRoot doesn't live under the expected home dir —
// the caller should already have validated this upstream.
func pathsUnderHome(username, docRoot string) []string {
	homeDir := "/home/" + username
	if !strings.HasPrefix(docRoot, homeDir+"/") {
		return nil
	}
	var paths []string
	cur := docRoot
	for cur != homeDir && cur != "/" && cur != "." {
		paths = append(paths, cur)
		cur = filepath.Dir(cur)
	}
	return paths
}

// writeVhost generates and writes the nginx vhost configuration, then tests and reloads nginx.
// This is the core logic shared by domain.create and domain.enable/disable.
// If the config content is unchanged, nginx reload is skipped for efficiency.
func writeVhost(ctx context.Context, username, domain, docRoot, phpVersion, customDirectives string, isEnabled bool) (string, error) {
	// Generate vhost configuration
	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		return "", fmt.Errorf("template parse failed: %w", err)
	}

	vhostData := vhostData{
		Domain:           domain,
		DocRoot:          docRoot,
		PHPVersion:       phpVersion,
		Username:         username,
		CustomDirectives: customDirectives,
		IsEnabled:        isEnabled,
	}

	var vhostConfig bytes.Buffer
	if err := tmpl.Execute(&vhostConfig, vhostData); err != nil {
		return "", fmt.Errorf("template execute failed: %w", err)
	}

	// Write vhost configuration atomically (temp file + rename)
	configPath := filepath.Join("/etc/nginx/sites-available", domain+".conf")
	tmpFile := configPath + ".tmp"

	if err := os.WriteFile(tmpFile, vhostConfig.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write config failed: %w", err)
	}

	if err := os.Rename(tmpFile, configPath); err != nil {
		os.Remove(tmpFile)
		return "", fmt.Errorf("rename config failed: %w", err)
	}

	// Create symlink to sites-enabled (always present for enabled or disabled domains)
	enabledPath := filepath.Join("/etc/nginx/sites-enabled", domain+".conf")
	os.Remove(enabledPath) // Remove if already exists
	if err := os.Symlink(configPath, enabledPath); err != nil {
		return "", fmt.Errorf("symlink failed: %w", err)
	}

	// Test nginx configuration
	testCmd := exec.CommandContext(ctx, "nginx", "-t")
	var testOutput bytes.Buffer
	testCmd.Stdout = &testOutput
	testCmd.Stderr = &testOutput
	if err := testCmd.Run(); err != nil {
		// Clean up on test failure
		os.Remove(enabledPath)
		os.Remove(configPath)
		return "", fmt.Errorf("nginx test failed: %s", testOutput.String())
	}

	// Reload nginx
	reloadCmd := exec.CommandContext(ctx, "systemctl", "reload", "nginx")
	var reloadOutput bytes.Buffer
	reloadCmd.Stdout = &reloadOutput
	reloadCmd.Stderr = &reloadOutput
	if err := reloadCmd.Run(); err != nil {
		return "", fmt.Errorf("systemctl reload nginx failed: %s", reloadOutput.String())
	}

	return configPath, nil
}

func domainCreateHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p domainCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate domain format
	if !domainRegex.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid domain %q: must match ^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$", p.Domain),
		}
	}

	// Validate username format
	if !usernameRegex.MatchString(p.Username) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid username %q: must match ^[a-z_][a-z0-9_-]{0,31}$", p.Username),
		}
	}

	// Validate doc_root starts with /home/
	if !bytes.HasPrefix([]byte(p.DocRoot), []byte("/home/")) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid doc_root %q: must start with /home/", p.DocRoot),
		}
	}

	// Create doc_root directory
	mkdirCmd := exec.CommandContext(ctx, "mkdir", "-p", p.DocRoot)
	if err := mkdirCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("mkdir failed: %v", err),
		}
	}

	// Chown + chmod every directory we created under /home/<user>/,
	// not just the leaf. mkdir -p would have left intermediates as
	// root:root; fix them so tenant data stays owned by the tenant.
	for _, dir := range pathsUnderHome(p.Username, p.DocRoot) {
		if err := exec.CommandContext(ctx, "chown", p.Username+":www-data", dir).Run(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chown %s: %v", dir, err),
			}
		}
		if err := exec.CommandContext(ctx, "chmod", "0750", dir).Run(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("chmod %s: %v", dir, err),
			}
		}
	}

	// Write a default index.html so a fresh domain serves a welcome
	// page instead of nginx's 403 on empty directory. Idempotent: if
	// the file already exists (e.g. the user uploaded real content, or
	// the reconciler re-ran domain.create), leave it alone.
	indexPath := filepath.Join(p.DocRoot, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		if werr := writeDefaultIndex(ctx, indexPath, p.Username, p.Domain, p.DocRoot); werr != nil {
			// non-fatal — the vhost still works, it'll just 403 until
			// the user uploads content.
			log.Printf("domain.create: failed to write default index.html for %s: %v", p.Domain, werr)
		}
	}

	// Default IsEnabled to true if not provided (backwards compatibility)
	isEnabled := true
	if p.IsEnabled != nil {
		isEnabled = *p.IsEnabled
	}

	configPath, err := writeVhost(ctx, p.Username, p.Domain, p.DocRoot, p.PHPVersion, p.CustomDirectives, isEnabled)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: err.Error(),
		}
	}

	return domainCreateResponse{
		Domain:     p.Domain,
		DocRoot:    p.DocRoot,
		ConfigPath: configPath,
	}, nil
}

func writeDefaultIndex(ctx context.Context, path, username, domain, docRoot string) error {
	const tmpl = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>{{.Domain}}</title>
  <style>
    body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; max-width: 640px; margin: 4rem auto; padding: 0 1.25rem; color: #222; line-height: 1.5; }
    h1 { color: #1976d2; margin-bottom: 0.25em; }
    .muted { color: #666; margin-top: 0; }
    code { background: #f5f5f5; padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.92em; font-family: ui-monospace, Menlo, Consolas, monospace; }
    hr { border: none; border-top: 1px solid #eee; margin: 2rem 0; }
    small { color: #888; }
  </style>
</head>
<body>
  <h1>{{.Domain}}</h1>
  <p class="muted">This domain is hosted by Jabali Panel. The site is provisioned and waiting for your content.</p>
  <hr>
  <p>Upload your files to the document root:</p>
  <p><code>{{.DocRoot}}</code></p>
  <p><small>Logged in as <code>{{.Username}}</code>.</small></p>
</body>
</html>
`

	t, err := template.New("index").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	// Write to a temp file first, then rename — avoids a half-written
	// index.html if we crash mid-write.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index.*.html.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // cleanup if rename fails

	data := struct {
		Domain, Username, DocRoot string
	}{Domain: domain, Username: username, DocRoot: docRoot}
	if err := t.Execute(tmp, data); err != nil {
		tmp.Close()
		return fmt.Errorf("execute template: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	// Set permissions + ownership before rename so the final file
	// is correct from the first ls. 0644 for world-readable by nginx.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// Chown to <user>:www-data so it matches the docroot's ownership.
	// Using exec.Command since os.Chown needs numeric IDs we don't have.
	if err := exec.CommandContext(ctx, "chown", username+":www-data", tmpName).Run(); err != nil {
		return fmt.Errorf("chown: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func init() {
	Default.Register("domain.create", domainCreateHandler)
}
