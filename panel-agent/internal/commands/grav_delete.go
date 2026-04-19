package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type gravDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type gravDeleteResp struct {
	Status string `json:"status"`
}

// gravTopLevel lists every entry the upstream grav-admin distribution
// drops at the install root after the unzip+flatten step.
//
// Generated from Grav 1.7.45. Bump if a future release adds entries.
var gravTopLevel = []string{
	".dependencies",
	".gitignore",
	".htaccess",
	"assets",
	"backup",
	"bin",
	"cache",
	"CHANGELOG.md",
	"CODE_OF_CONDUCT.md",
	"composer.json",
	"composer.lock",
	"CONTRIBUTING.md",
	"images",
	"index.php",
	"LICENSE.txt",
	"logs",
	"nginx.conf",
	"README.md",
	"robots.txt",
	"system",
	"tmp",
	"user",
	"vendor",
	"web.config",
	"webserver-configs",
}

func gravDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req gravDeleteReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("failed to parse params: %v", err)}
	}
	if req.OSUser == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "os_user is required"}
	}
	if req.Docroot == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "docroot is required"}
	}
	if err := validateDocrootPath(req.OSUser, req.Docroot); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("invalid docroot: %v", err)}
	}

	installPath := computeGravInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range gravTopLevel {
			cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", filepath.Join(installPath, name))
			_ = cmd.Run()
		}
	}

	if req.Subdirectory == "" && req.Domain != "" {
		indexPath := filepath.Join(req.Docroot, "index.html")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			_ = writeDefaultIndex(ctx, indexPath, req.OSUser, req.Domain, req.Docroot)
		}
	}

	return gravDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("grav", gravDeleteHandler)
}
