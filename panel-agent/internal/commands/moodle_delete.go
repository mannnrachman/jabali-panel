package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type moodleDeleteReq struct {
	AppType      string `json:"app_type"`
	InstallID    string `json:"install_id"` // M19 framework: needed to recompute moodledata path
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type moodleDeleteResp struct {
	Status string `json:"status"`
}

// moodleTopLevel lists every entry the upstream Moodle tarball lays
// down at the install root after `tar --strip-components=1`, plus
// config.php which the CLI installer generates.
//
// Generated from Moodle 4.4.2.
var moodleTopLevel = []string{
	".grunt",
	".htaccess",
	"admin",
	"analytics",
	"auth",
	"availability",
	"backup",
	"badges",
	"blocks",
	"blog",
	"brokenfile.php",
	"cache",
	"calendar",
	"cohort",
	"comment",
	"competency",
	"completion",
	"composer.json",
	"composer.lock",
	"config-dist.php",
	"config.php",
	"contentbank",
	"course",
	"customfield",
	"draftfile.php",
	"enrol",
	"error",
	"favourites",
	"file.php",
	"files",
	"filter",
	"grade",
	"group",
	"h5p",
	"help.php",
	"help_ajax.php",
	"index.php",
	"install",
	"install.php",
	"INSTALL.txt",
	"iplookup",
	"lib",
	"local",
	"login",
	"media",
	"message",
	"mod",
	"my",
	"notes",
	"npm-shrinkwrap.json",
	"package.json",
	"payment",
	"pix",
	"plagiarism",
	"pluginfile.php",
	"portfolio",
	"privacy",
	"public.api.php",
	"question",
	"rating",
	"README.txt",
	"report",
	"reportbuilder",
	"repository",
	"robots.txt",
	"rss",
	"search",
	"tag",
	"tag.php",
	"theme",
	"TRADEMARK.txt",
	"user",
	"userpix",
	"webservice",
}

func moodleDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req moodleDeleteReq
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

	installPath := computeMoodleInstallPath(req.Docroot, req.Subdirectory)

	// Filesystem cleanup of the docroot side.
	if req.Subdirectory != "" {
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		for _, name := range moodleTopLevel {
			cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", filepath.Join(installPath, name))
			_ = cmd.Run()
		}
	}

	// Managed-data-dir cleanup (M19 framework). Skipped if install_id
	// is missing — that signals a legacy delete from a panel build
	// that didn't plumb install_id through, in which case the operator
	// has to clean the moodledata dir manually.
	if req.InstallID != "" {
		_ = removeManagedDataDir(ctx, req.OSUser, req.InstallID)
	}

	if req.Subdirectory == "" && req.Domain != "" {
		indexPath := filepath.Join(req.Docroot, "index.html")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			_ = writeDefaultIndex(ctx, indexPath, req.OSUser, req.Domain, req.Docroot)
		}
	}

	return moodleDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("moodle", moodleDeleteHandler)
}
