package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainCreateParams is the input shape for domain.create.
type domainCreateParams struct {
	Username           string `json:"username"`
	Domain             string `json:"domain"`
	DocRoot            string `json:"doc_root"`
	HasPHP             bool   `json:"has_php"`
	PHPVersion         string `json:"php_version"`
	CustomDirectives   string `json:"custom_directives"`
	RedirectDirectives string `json:"redirect_directives"`
	RuleDirectives     string `json:"rule_directives"`
	IndexPriority      string `json:"index_priority"`
	// IsEnabled controls whether the vhost serves the tenant's docroot
	// (true) or a branded "site disabled" placeholder (false). Pointer
	// so omitted fields default to true (backwards compat).
	IsEnabled    *bool  `json:"is_enabled"`
	SSLCertPath  string `json:"ssl_cert_path"`
	SSLKeyPath   string `json:"ssl_key_path"`
	// PHP INI overrides: omitted if not set on the domain.
	PHPMemoryLimit       string `json:"php_memory_limit,omitempty"`
	PHPUploadMaxFilesize string `json:"php_upload_max_filesize,omitempty"`
	PHPPostMaxSize       string `json:"php_post_max_size,omitempty"`
	PHPMaxInputVars      int    `json:"php_max_input_vars,omitempty"`
	PHPMaxExecutionTime  int    `json:"php_max_execution_time,omitempty"`
	PHPMaxInputTime      int    `json:"php_max_input_time,omitempty"`
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
    server_name {{.Domain}} www.{{.Domain}};
{{ if .SSLCertPath }}
    # Redirect HTTP to HTTPS when SSL is configured
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name {{.Domain}} www.{{.Domain}};
    ssl_certificate {{.SSLCertPath}};
    ssl_certificate_key {{.SSLKeyPath}};
{{ end }}
{{ if .IsEnabled }}
    root {{.DocRoot}};
    {{.IndexDirective}}

    location / {
{{ if .HasPHP }}
        try_files $uri $uri/ /index.php?$query_string;
{{ else }}
        try_files $uri $uri/ =404;
{{ end }}
    }

{{ if .HasPHP }}
    location ~ \.php$ {
        fastcgi_pass unix:/run/php/jabali-{{.Username}}/fpm.sock;
        fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
        include fastcgi_params;
{{ if .PHPValueParam }}
        fastcgi_param PHP_VALUE "{{.PHPValueParam}}";
{{ end }}
    }
{{ end }}

    access_log /var/log/nginx/{{.Domain}}-access.log;
    error_log /var/log/nginx/{{.Domain}}-error.log;

    {{.RedirectDirectives}}
    {{.RuleDirectives}}
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
}`

type vhostData struct {
	Domain               string
	DocRoot              string
	HasPHP               bool
	PHPVersion           string
	Username             string
	IndexDirective       string
	RedirectDirectives   string
	RuleDirectives       string
	CustomDirectives     string
	IsEnabled            bool
	SSLCertPath          string
	SSLKeyPath           string
	PHPMemoryLimit       string
	PHPUploadMaxFilesize string
	PHPPostMaxSize       string
	PHPMaxInputVars      int
	PHPMaxExecutionTime  int
	PHPMaxInputTime      int
	PHPValueParam        string // fastcgi_param PHP_VALUE directive content
}

// indexDirectiveFor maps the panel's index_priority enum to the concrete
// nginx `index ...;` directive. Unknown values fall back to the prior
// hardcoded behaviour so a mis-plumbed reconciler can't silently break
// a tenant's site.
func indexDirectiveFor(priority string) string {
	switch priority {
	case "php_first":
		return "index index.php index.html;"
	case "html_only":
		return "index index.html;"
	case "php_only":
		return "index index.php;"
	case "full":
		return "index index.php index.html index.htm;"
	case "html_first", "":
		fallthrough
	default:
		return "index index.html index.php;"
	}
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

// buildPHPValueParam assembles a fastcgi_param PHP_VALUE directive from per-domain INI overrides.
// Returns empty string if all overrides are empty/zero. Format: key1=val1\nkey2=val2\n
func buildPHPValueParam(memLimit, uploadMax, postMax string, maxInputVars, maxExecTime, maxInputTime int) string {
	var parts []string
	if memLimit != "" {
		parts = append(parts, "memory_limit="+memLimit)
	}
	if uploadMax != "" {
		parts = append(parts, "upload_max_filesize="+uploadMax)
	}
	if postMax != "" {
		parts = append(parts, "post_max_size="+postMax)
	}
	if maxInputVars > 0 {
		parts = append(parts, "max_input_vars="+strconv.Itoa(maxInputVars))
	}
	if maxExecTime > 0 {
		parts = append(parts, "max_execution_time="+strconv.Itoa(maxExecTime))
	}
	if maxInputTime > 0 {
		parts = append(parts, "max_input_time="+strconv.Itoa(maxInputTime))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// writeVhost generates and writes the nginx vhost configuration, then tests and reloads nginx.
// This is the core logic shared by domain.create and domain.enable/disable.
// If the config content is unchanged, nginx reload is skipped for efficiency.
func writeVhost(ctx context.Context, username, domain, docRoot, phpVersion, redirectDirectives, ruleDirectives, customDirectives, indexPriority string, isEnabled, hasPHP bool, sslCertPath, sslKeyPath, phpMemLimit, phpUploadMax, phpPostMax string, phpMaxInputVars, phpMaxExecTime, phpMaxInputTime int) (string, error) {
	// Generate vhost configuration
	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		return "", fmt.Errorf("template parse failed: %w", err)
	}

	vhostData := vhostData{
		Domain:             domain,
		DocRoot:            docRoot,
		HasPHP:             hasPHP,
		PHPVersion:         phpVersion,
		Username:           username,
		IndexDirective:     indexDirectiveFor(indexPriority),
		RedirectDirectives: redirectDirectives,
		RuleDirectives:     ruleDirectives,
		CustomDirectives:   customDirectives,
		IsEnabled:          isEnabled,
		SSLCertPath:        sslCertPath,
		SSLKeyPath:         sslKeyPath,
		PHPMemoryLimit:     phpMemLimit,
		PHPUploadMaxFilesize: phpUploadMax,
		PHPPostMaxSize:     phpPostMax,
		PHPMaxInputVars:    phpMaxInputVars,
		PHPMaxExecutionTime: phpMaxExecTime,
		PHPMaxInputTime:    phpMaxInputTime,
		PHPValueParam:      buildPHPValueParam(phpMemLimit, phpUploadMax, phpPostMax, phpMaxInputVars, phpMaxExecTime, phpMaxInputTime),
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
	//
	// Also skip when index.php exists — that's the unambiguous "real
	// content present" signal (WordPress install, hand-uploaded app).
	// Without this check, the reconciler periodically re-creates the
	// placeholder ~30s after a WP install, and nginx (with `index
	// index.html index.php`) serves the placeholder instead of WP.
	indexPath := filepath.Join(p.DocRoot, "index.html")
	indexPHPPath := filepath.Join(p.DocRoot, "index.php")
	_, htmlErr := os.Stat(indexPath)
	_, phpErr := os.Stat(indexPHPPath)
	if os.IsNotExist(htmlErr) && os.IsNotExist(phpErr) {
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

	configPath, err := writeVhost(ctx, p.Username, p.Domain, p.DocRoot, p.PHPVersion, p.RedirectDirectives, p.RuleDirectives, p.CustomDirectives, p.IndexPriority, isEnabled, p.HasPHP, p.SSLCertPath, p.SSLKeyPath, p.PHPMemoryLimit, p.PHPUploadMaxFilesize, p.PHPPostMaxSize, p.PHPMaxInputVars, p.PHPMaxExecutionTime, p.PHPMaxInputTime)
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
