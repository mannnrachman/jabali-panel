package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type concreteDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type concreteDeleteResp struct {
	Status string `json:"status"`
}

// concreteTopLevel lists every entry the upstream Concrete CMS zip
// lays down at the install root after the flatten step. Generated
// from Concrete CMS 9.3.4.
var concreteTopLevel = []string{
	".babelrc",
	".browserslistrc",
	".eslintrc",
	".gitignore",
	".htaccess",
	".prettierrc",
	"application",
	"build",
	"composer.json",
	"composer.lock",
	"concrete",
	"index.php",
	"LICENSE",
	"package.json",
	"packages",
	"README.md",
	"robots.txt",
	"updates",
}

func concreteDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req concreteDeleteReq
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

	installPath := computeConcreteInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range concreteTopLevel {
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

	return concreteDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("concrete", concreteDeleteHandler)
}
