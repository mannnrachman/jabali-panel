package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type backdropDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type backdropDeleteResp struct {
	Status string `json:"status"`
}

// backdropTopLevel lists every entry the upstream Backdrop zip lays
// down at the install root after the flatten step.
//
// Generated from Backdrop CMS 1.28.4.
var backdropTopLevel = []string{
	".editorconfig",
	".gitignore",
	".htaccess",
	"core",
	"CHANGELOG.md",
	"files",
	"index.php",
	"layouts",
	"LICENSE.txt",
	"modules",
	"README.md",
	"robots.txt",
	"settings.php",
	"sites",
	"themes",
}

func backdropDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req backdropDeleteReq
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

	installPath := computeBackdropInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range backdropTopLevel {
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

	return backdropDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("backdrop", backdropDeleteHandler)
}
