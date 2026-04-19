package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type matomoDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type matomoDeleteResp struct {
	Status string `json:"status"`
}

// matomoTopLevel lists every entry the upstream Matomo zip lays down at
// the install root (after the matomo/* flatten step). Generated from
// Matomo 5.2.0.
var matomoTopLevel = []string{
	".bowerrc",
	".htaccess",
	"bootstrap.php",
	"CHANGELOG.md",
	"CONTRIBUTING.md",
	"config",
	"console",
	"core",
	"DEVELOPER.md",
	"favicon.ico",
	"How to install Matomo.html",
	"index.php",
	"js",
	"LEGALNOTICE",
	"LICENSE",
	"libs",
	"matomo.js",
	"matomo.php",
	"misc",
	"node_modules",
	"piwik.js",
	"piwik.php",
	"plugins",
	"README.md",
	"robots.txt",
	"SECURITY.md",
	"tests",
	"tmp",
	"vendor",
	"vue",
}

func matomoDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req matomoDeleteReq
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

	installPath := computeMatomoInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range matomoTopLevel {
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

	return matomoDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("matomo", matomoDeleteHandler)
}
