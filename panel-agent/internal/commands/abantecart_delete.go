package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type abantecartDeleteReq struct {
	AppType      string `json:"app_type"`
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type abantecartDeleteResp struct {
	Status string `json:"status"`
}

// abantecartTopLevel lists every entry the upstream AbanteCart
// public_html/* drops into installPath after the flatten step.
//
// Generated from AbanteCart 1.4.0.
var abantecartTopLevel = []string{
	".htaccess",
	"admin",
	"core",
	"download",
	"extensions",
	"image",
	"index.php",
	"install",
	"resources",
	"robots.txt",
	"storefront",
	"system",
}

func abantecartDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req abantecartDeleteReq
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

	installPath := computeAbanteCartInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range abantecartTopLevel {
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

	return abantecartDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("abantecart", abantecartDeleteHandler)
}
