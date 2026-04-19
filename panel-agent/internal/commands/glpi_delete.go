package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type glpiDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type glpiDeleteResp struct {
	Status string `json:"status"`
}

// glpiTopLevel lists every entry the upstream GLPI tarball lays down
// at the install root after `tar --strip-components=1`.
//
// Generated from GLPI 10.0.16.
var glpiTopLevel = []string{
	".htaccess",
	"ajax",
	"api.php",
	"apirest.php",
	"apixmlrpc.php",
	"bin",
	"caldav.php",
	"check_csv_delimiter.php",
	"composer.json",
	"composer.lock",
	"config",
	"css",
	"CHANGELOG.md",
	"CONTRIBUTING.md",
	"COPYING.txt",
	"dependency_injection",
	"files",
	"front",
	"index.php",
	"inc",
	"install",
	"js",
	"lib",
	"locales",
	"marketplace",
	"node_modules",
	"package.json",
	"package-lock.json",
	"pics",
	"plugins",
	"public",
	"README.md",
	"resources",
	"robots.txt",
	"src",
	"status.php",
	"templates",
	"tests",
	"tools",
	"vendor",
	"webpack.config.js",
}

func glpiDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req glpiDeleteReq
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

	installPath := computeGLPIInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range glpiTopLevel {
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

	return glpiDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("glpi", glpiDeleteHandler)
}
