package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"text/template"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type runtimeApplyParams struct {
	Username    string            `json:"username"`
	Domain      string            `json:"domain"`
	DocRoot     string            `json:"doc_root"`
	RuntimeType string            `json:"runtime_type"`
	EntryPoint  string            `json:"entry_point"`
	ListenPort  int               `json:"listen_port"`
	EnvVars     map[string]string `json:"env_vars"`
	IsEnabled   bool              `json:"is_enabled"`
}

type runtimeApplyResponse struct {
	ServicePath string `json:"service_path"`
	NoChange    bool   `json:"no_change,omitempty"`
}

const serviceTemplate = `[Unit]
Description=Jabali Managed Service - {{.Domain}} ({{.RuntimeType}})
After=network.target

[Service]
Type=simple
{{if .IsDocker -}}
ExecStartPre=-/usr/bin/sg docker -c '{{.DockerExecutable}} rm -f jabali-rt-{{.Domain}}'
ExecStart=/usr/bin/sg docker -c '{{.DockerExecutable}} run --name jabali-rt-{{.Domain}} --rm -p 127.0.0.1:{{.ListenPort}}:{{.ContainerPort}} {{.ImageName}}'
ExecStop=/usr/bin/sg docker -c '{{.DockerExecutable}} stop -t 2 jabali-rt-{{.Domain}}'
{{- else -}}
WorkingDirectory={{.DocRoot}}
ExecStart={{.Executable}} {{.Args}}
NoNewPrivileges=true
PrivateTmp=true
{{- end}}
Restart=always
RestartSec=5s
Environment=PORT={{.ListenPort}}
{{if .EnvFilePath}}EnvironmentFile=-{{.EnvFilePath}}
{{end -}}
# Safe default PATH
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/home/{{.Username}}/.local/bin

[Install]
WantedBy=default.target
`

type vhostServiceData struct {
	Domain           string
	RuntimeType      string
	Username         string
	DocRoot          string
	Executable       string
	Args             string
	ListenPort       int
	EnvVars          map[string]string
	EnvFilePath      string
	IsDocker         bool
	DockerExecutable string
	ContainerPort    int
	ImageName        string
}

func runtimeApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p runtimeApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	if p.Username == "" || p.Domain == "" || p.DocRoot == "" || p.RuntimeType == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username, domain, doc_root, and runtime_type are required",
		}
	}

	// Defense in depth: the agent renders these values into a systemd
	// unit file and (for docker) a `docker run` command line, so it
	// validates independently of the panel-api. Never trust the caller.
	if err := validateRuntimeType(p.RuntimeType); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateSafeToken("username", p.Username); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateSafeToken("domain", p.Domain); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateEntryPoint(p.EntryPoint); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	// Docroot must live under /home/<user>/domains/ — the systemd unit's
	// WorkingDirectory points here, so reject arbitrary host paths.
	if err := validateDocrootPath(p.Username, p.DocRoot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateEnvVars(p.EnvVars); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	// Resolve user's UID and runtime directory
	u, err := user.Lookup(p.Username)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("user %s not found: %v", p.Username, err),
		}
	}

	uid, _ := strconv.Atoi(u.Uid)
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)

	// Ensure systemd user config directory exists
	userSystemdDir := filepath.Join(u.HomeDir, ".config", "systemd", "user")
	// Make directory as target user to ensure correct ownership
	if _, err := os.Stat(userSystemdDir); os.IsNotExist(err) {
		mkdirCmd := exec.CommandContext(ctx, "sudo", "-u", p.Username, "mkdir", "-p", userSystemdDir)
		if err := mkdirCmd.Run(); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to create systemd user dir: %v", err),
			}
		}
	}

	serviceName := fmt.Sprintf("jabali-rt-%s.service", p.Domain)
	servicePath := filepath.Join(userSystemdDir, serviceName)
	// User-supplied env vars are written to a separate EnvironmentFile
	// rather than inline Environment= directives. This keeps untrusted
	// values out of the unit file body entirely, so they can never
	// inject new unit directives even if validation is bypassed.
	envFileName := fmt.Sprintf("jabali-rt-%s.env", p.Domain)
	envFilePath := filepath.Join(userSystemdDir, envFileName)

	// Generate systemd service content
	var exe, args, dockerExe, imageName string
	var containerPort int = 8080
	isDocker := p.RuntimeType == "docker"

	if isDocker {
		dockerExe = resolveExecutable(p.Username, "docker")
		imageName = p.EntryPoint
		if imageName == "" {
			imageName = "app"
		}
		// The image name is rendered as a bare token in the systemd
		// ExecStart line. Reject anything that could inject extra
		// `docker run` flags (e.g. --privileged, -v /:/host).
		if err := validateImageName(imageName); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
		}
		// Try to read custom container port from env if present
		if customPortStr, ok := p.EnvVars["CONTAINER_PORT"]; ok {
			if cp, err := strconv.Atoi(customPortStr); err == nil {
				containerPort = cp
			}
		}
	} else {
		exe = resolveExecutable(p.Username, p.RuntimeType)
		switch p.RuntimeType {
		case "nodejs":
			entry := p.EntryPoint
			if entry == "" {
				entry = "index.js"
			}
			args = filepath.Join(p.DocRoot, entry)
		case "python":
			entry := p.EntryPoint
			if entry == "" {
				entry = "app.py"
			}
			args = filepath.Join(p.DocRoot, entry)
		case "go":
			// Go compiled binary is named 'server' by deploy step
			exe = filepath.Join(p.DocRoot, "server")
			args = ""
		}
	}

	// Render the EnvironmentFile body (KEY=VALUE per line). Values were
	// already validated (no newlines/NULs) so each pair stays on one
	// line. Empty when there are no user env vars, in which case the
	// unit omits the EnvironmentFile directive entirely.
	var envFileBody []byte
	var envFileForUnit string
	if len(p.EnvVars) > 0 {
		keys := make([]string, 0, len(p.EnvVars))
		for k := range p.EnvVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var eb bytes.Buffer
		for _, k := range keys {
			fmt.Fprintf(&eb, "%s=%s\n", k, p.EnvVars[k])
		}
		envFileBody = eb.Bytes()
		envFileForUnit = envFilePath
	}

	data := vhostServiceData{
		Domain:           p.Domain,
		RuntimeType:      p.RuntimeType,
		Username:         p.Username,
		DocRoot:          p.DocRoot,
		Executable:       exe,
		Args:             args,
		ListenPort:       p.ListenPort,
		EnvVars:          p.EnvVars,
		EnvFilePath:      envFileForUnit,
		IsDocker:         isDocker,
		DockerExecutable: dockerExe,
		ContainerPort:    containerPort,
		ImageName:        imageName,
	}

	tmpl, err := template.New("service").Parse(serviceTemplate)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to parse service template: %v", err),
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to render service template: %v", err),
		}
	}

	content := buf.Bytes()

	// Check if already matches to prevent redundant reloads. The unit
	// file references the EnvironmentFile by path, so an env-only change
	// won't alter `content`; compare the env file body separately so an
	// env edit still triggers a rewrite + restart.
	noChange := false
	existingContent, err := os.ReadFile(servicePath)
	unitPreexisting := err == nil
	if err == nil && bytes.Equal(existingContent, content) {
		existingEnv, _ := os.ReadFile(envFilePath)
		if bytes.Equal(existingEnv, envFileBody) {
			noChange = true
		}
	}

	// On a FIRST-TIME install of an enabled service, verify the allocated
	// port isn't already bound by some unrelated host process. The panel's
	// allocator only checks panel-managed rows, so a non-jabali listener
	// would otherwise surface as an opaque crash loop. We skip this when
	// the unit already exists (a restart of our own service would see its
	// own socket as "in use") and for docker (the daemon owns the bind).
	if p.IsEnabled && !unitPreexisting && !isDocker && portInUseOnHost(p.ListenPort) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeFailedPrecondition,
			Message: fmt.Sprintf("listen port %d is already in use on the host", p.ListenPort),
		}
	}

	if !noChange {
		// Write/refresh the EnvironmentFile first so the unit it
		// references always exists by the time systemd reads it.
		if len(envFileBody) > 0 {
			if err := writeUserFile(ctx, p.Username, userSystemdDir, envFileName, envFileBody, 0600); err != nil {
				return nil, &agentwire.AgentError{
					Code:    agentwire.CodeInternal,
					Message: fmt.Sprintf("failed to write env file: %v", err),
				}
			}
		} else {
			_ = os.Remove(envFilePath)
		}

		// Write the service file
		tmpFile := filepath.Join(userSystemdDir, fmt.Sprintf(".tmp-%s", serviceName))
		if err := os.WriteFile(tmpFile, content, 0644); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to write temp service file: %v", err),
			}
		}

		// Chown file to user
		chownCmd := exec.CommandContext(ctx, "chown", p.Username+":"+p.Username, tmpFile)
		if err := chownCmd.Run(); err != nil {
			os.Remove(tmpFile)
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to chown service file: %v", err),
			}
		}

		// Rename atomically
		if err := os.Rename(tmpFile, servicePath); err != nil {
			os.Remove(tmpFile)
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to place service file: %v", err),
			}
		}

		// Daemon reload as user
		if err := systemctlUserExec(ctx, p.Username, runtimeDir, "daemon-reload"); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to reload systemd daemon: %v", err),
			}
		}
	}

	// Apply enabled/started state
	if p.IsEnabled {
		// Enable the service
		if err := systemctlUserExec(ctx, p.Username, runtimeDir, "enable", serviceName); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to enable service: %v", err),
			}
		}
		// Start or restart if changed
		if err := systemctlUserExec(ctx, p.Username, runtimeDir, "restart", serviceName); err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("failed to restart service: %v", err),
			}
		}
	} else {
		// Stop the service
		_ = systemctlUserExec(ctx, p.Username, runtimeDir, "stop", serviceName)
		// Disable the service
		_ = systemctlUserExec(ctx, p.Username, runtimeDir, "disable", serviceName)
	}

	return &runtimeApplyResponse{
		ServicePath: servicePath,
		NoChange:    noChange,
	}, nil
}

func init() {
	Default.Register("runtime.apply", runtimeApplyHandler)
}
