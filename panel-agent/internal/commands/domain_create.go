package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"text/template"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainCreateParams is the input shape for domain.create.
type domainCreateParams struct {
	Username   string `json:"username"`
	Domain     string `json:"domain"`
	DocRoot    string `json:"doc_root"`
	PHPVersion string `json:"php_version"`
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
}
`

type vhostData struct {
	Domain           string
	DocRoot          string
	PHPVersion       string
	Username         string
	CustomDirectives string
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

	// Chown docroot to <user>:www-data so nginx can read files via group
	// perms while the user retains full ownership.
	chownCmd := exec.CommandContext(ctx, "chown", p.Username+":www-data", p.DocRoot)
	if err := chownCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chown failed: %v", err),
		}
	}

	// 0750: owner (user) rwx, group (www-data) rx, others nothing.
	chmodCmd := exec.CommandContext(ctx, "chmod", "0750", p.DocRoot)
	if err := chmodCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("docroot chmod failed: %v", err),
		}
	}

	// Generate vhost configuration
	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("template parse failed: %v", err),
		}
	}

	vhostData := vhostData{
		Domain:           p.Domain,
		DocRoot:          p.DocRoot,
		PHPVersion:       p.PHPVersion,
		Username:         p.Username,
		CustomDirectives: "",
	}

	var vhostConfig bytes.Buffer
	if err := tmpl.Execute(&vhostConfig, vhostData); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("template execute failed: %v", err),
		}
	}

	// Write vhost configuration
	configPath := filepath.Join("/etc/nginx/sites-available", p.Domain+".conf")
	if err := os.WriteFile(configPath, vhostConfig.Bytes(), 0644); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("write config failed: %v", err),
		}
	}

	// Create symlink to sites-enabled
	enabledPath := filepath.Join("/etc/nginx/sites-enabled", p.Domain+".conf")
	// Remove existing symlink if present
	os.Remove(enabledPath)
	if err := os.Symlink(configPath, enabledPath); err != nil {
		// Clean up config file on failure
		os.Remove(configPath)
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("symlink failed: %v", err),
		}
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
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("nginx test failed: %s", testOutput.String()),
		}
	}

	// Reload nginx
	reloadCmd := exec.CommandContext(ctx, "systemctl", "reload", "nginx")
	var reloadOutput bytes.Buffer
	reloadCmd.Stdout = &reloadOutput
	reloadCmd.Stderr = &reloadOutput
	if err := reloadCmd.Run(); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("systemctl reload nginx failed: %s", reloadOutput.String()),
		}
	}

	return domainCreateResponse{
		Domain:     p.Domain,
		DocRoot:    p.DocRoot,
		ConfigPath: configPath,
	}, nil
}

func init() {
	Default.Register("domain.create", domainCreateHandler)
}
