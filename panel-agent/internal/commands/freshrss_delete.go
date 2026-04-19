package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type freshrssDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type freshrssDeleteResp struct {
	Status string `json:"status"`
}

// freshrssTopLevel lists every entry the upstream FreshRSS GitHub
// release archive lays down at the install root after `tar --strip-
// components=1`.
//
// Generated from FreshRSS 1.24.1.
var freshrssTopLevel = []string{
	".dockerignore",
	".editorconfig",
	".gitattributes",
	".github",
	".gitignore",
	".htaccess",
	".vscode",
	"app",
	"cli",
	"CODE_OF_CONDUCT.md",
	"COPYING",
	"COPYRIGHT",
	"data",
	"Docker",
	"FAQ.md",
	"i",
	"index.php",
	"lib",
	"LICENSE",
	"NEWS",
	"p",
	"README.md",
	"SECURITY.md",
	"thirdparty",
	"vendor",
}

func freshrssDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req freshrssDeleteReq
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

	installPath := computeFreshRSSInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range freshrssTopLevel {
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

	return freshrssDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("freshrss", freshrssDeleteHandler)
}
