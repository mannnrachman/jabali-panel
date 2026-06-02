package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type runtimeDeployParams struct {
	Username    string `json:"username"`
	DocRoot     string `json:"doc_root"`
	RuntimeType string `json:"runtime_type"`
	EntryPoint  string `json:"entry_point"`
}

type runtimeDeployResponse struct {
	Output string `json:"output"`
}

func sudoUserEnvCommand(ctx context.Context, username string, env []string, argv ...string) *exec.Cmd {
	args := []string{"-u", username}
	if len(env) > 0 {
		args = append(args, "env")
		args = append(args, env...)
	}
	args = append(args, argv...)
	return exec.CommandContext(ctx, "sudo", args...)
}

func runtimeDeployHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p runtimeDeployParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	if p.Username == "" || p.DocRoot == "" || p.RuntimeType == "" {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "username, doc_root, and runtime_type are required",
		}
	}

	// Defense in depth: validate before any value reaches a command line.
	if err := validateRuntimeType(p.RuntimeType); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateSafeToken("username", p.Username); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateEntryPoint(p.EntryPoint); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	// Docroot must live under /home/<user>/domains/ (same guard the CMS
	// install handlers use). Prevents pointing the WorkingDirectory or
	// build step at an arbitrary host path.
	if err := validateDocrootPath(p.Username, p.DocRoot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	// Make sure the docroot exists
	if _, err := os.Stat(p.DocRoot); os.IsNotExist(err) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeNotFound,
			Message: fmt.Sprintf("doc_root %s does not exist", p.DocRoot),
		}
	}

	var cmd *exec.Cmd
	var stdout, stderr bytes.Buffer

	switch p.RuntimeType {
	case "nodejs":
		pkgJSON := filepath.Join(p.DocRoot, "package.json")
		if _, err := os.Stat(pkgJSON); err == nil {
			npmPath := resolveExecutable(p.Username, "npm")
			args := []string{npmPath, "install", "--no-audit", "--no-fund"}
			if _, lockErr := os.Stat(filepath.Join(p.DocRoot, "package-lock.json")); lockErr == nil {
				args = []string{npmPath, "ci", "--no-audit", "--no-fund"}
			}
			cmd = sudoUserEnvCommand(ctx, p.Username, []string{
				fmt.Sprintf("HOME=/home/%s", p.Username),
				fmt.Sprintf("PATH=%s:%s", filepath.Dir(npmPath), os.Getenv("PATH")),
			}, args...)
		} else {
			return &runtimeDeployResponse{Output: "No package.json found; skipping npm install."}, nil
		}
	case "python":
		reqTxt := filepath.Join(p.DocRoot, "requirements.txt")
		if _, err := os.Stat(reqTxt); err == nil {
			pythonPath := resolveExecutable(p.Username, "python3")
			installEnv := []string{
				fmt.Sprintf("HOME=/home/%s", p.Username),
				fmt.Sprintf("PATH=%s:%s", filepath.Dir(pythonPath), os.Getenv("PATH")),
			}
			cmd = sudoUserEnvCommand(ctx, p.Username, installEnv, pythonPath, "-m", "pip", "install", "--user", "--break-system-packages", "-r", "requirements.txt")
		} else {
			return &runtimeDeployResponse{Output: "No requirements.txt found; skipping pip install."}, nil
		}
	case "go":
		entry := p.EntryPoint
		if entry == "" {
			entry = "main.go"
		}
		goPath := resolveExecutable(p.Username, "go")
		cmd = sudoUserEnvCommand(ctx, p.Username, []string{fmt.Sprintf("HOME=/home/%s", p.Username)}, goPath, "build", "-o", "server", entry)
	case "docker":
		dockerfile := filepath.Join(p.DocRoot, "Dockerfile")
		dockerPath := resolveExecutable(p.Username, "docker")
		if _, err := os.Stat(dockerfile); err == nil {
			tag := strings.ToLower(p.EntryPoint)
			if tag == "" {
				tag = "app"
			}
			// tag / image ref is passed as an argv element (not a shell
			// string) but we still reject metacharacters so it can't be
			// misread as a flag or smuggle args into a later apply step.
			if err := validateImageName(tag); err != nil {
				return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
			}
			cmd = sudoUserEnvCommand(ctx, p.Username, []string{fmt.Sprintf("HOME=/home/%s", p.Username)}, dockerPath, "build", "-t", tag, ".")
		} else if p.EntryPoint != "" {
			if err := validateImageName(p.EntryPoint); err != nil {
				return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
			}
			cmd = sudoUserEnvCommand(ctx, p.Username, []string{fmt.Sprintf("HOME=/home/%s", p.Username)}, dockerPath, "pull", p.EntryPoint)
		} else {
			return &runtimeDeployResponse{Output: "No Dockerfile or image specified; skipping deploy."}, nil
		}
	default:
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("unsupported runtime_type %q", p.RuntimeType),
		}
	}

	if cmd != nil {
		cmd.Dir = p.DocRoot
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		outStr := stdout.String() + "\n" + stderr.String()
		if err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("deploy execution failed: %v\nOutput:\n%s", err, strings.TrimSpace(outStr)),
			}
		}
		return &runtimeDeployResponse{Output: strings.TrimSpace(outStr)}, nil
	}

	return &runtimeDeployResponse{Output: "No action taken"}, nil
}

func init() {
	Default.Register("runtime.deploy", runtimeDeployHandler)
}
