package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// dokuwikiDeleteReq is the input shape for the DokuWiki deleter.
// Mirrors wordpressDeleteReq plus an optional Subdirectory: when the
// install lives at <docroot>/<subdir>/ the deleter wipes only that
// subtree, leaving the docroot itself (and any sibling installs)
// alone. An empty subdirectory means "deleted-from-docroot" — same
// semantics as the WP installer.
type dokuwikiDeleteReq struct {
	AppType      string `json:"app_type"`     // present, ignored
	OSUser       string `json:"os_user"`
	Docroot      string `json:"docroot"`
	Subdirectory string `json:"subdirectory,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

type dokuwikiDeleteResp struct {
	Status string `json:"status"`
}

// dokuwikiTopLevel lists every entry the upstream stable tarball lays
// down at the install root after `tar --strip-components=1`. We
// enumerate them rather than `rm -rf <installPath>` so a docroot
// install (subdir="") doesn't accidentally take out a hand-uploaded
// .htaccess or unrelated files the user dropped next to DokuWiki.
//
// Generated from the dokuwiki-2024-02-06b "Kaos" tarball; bump if a
// future stable adds a top-level entry. Entries that don't exist on
// disk are skipped silently.
var dokuwikiTopLevel = []string{
	"bin",
	"conf",
	"data",
	"doku.php",
	"feed.php",
	"inc",
	"index.php",
	"install.lock",
	"install.php",
	"lib",
	"COPYING",
	"README",
	"VERSION",
}

func dokuwikiDeleteHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var req dokuwikiDeleteReq
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
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

	installPath := computeDokuWikiInstallPath(req.Docroot, req.Subdirectory)

	if req.Subdirectory != "" {
		// Subdir install: wipe the entire subtree as the install user.
		// validateDocrootPath above proved Docroot is inside the user's
		// /home/<user>/domains/ tree; subdirectory came from the panel
		// (server-side regex enforced) so the join can't escape upward.
		cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", installPath)
		_ = cmd.Run()
	} else {
		// Docroot install: enumerate the upstream tarball's top-level
		// entries one by one so unrelated files (e.g. a user's static
		// .well-known/ or a pre-existing .htaccess) survive.
		for _, name := range dokuwikiTopLevel {
			cmd := buildSystemdRunCmd(ctx, req.OSUser, "rm", "-rf", filepath.Join(installPath, name))
			_ = cmd.Run()
		}
	}

	// Restore the domain.create placeholder when the docroot is now
	// empty — only for docroot installs (subdir installs leave the
	// docroot untouched). Mirrors wordpress_delete's behaviour so
	// nginx doesn't 403 after the delete completes.
	if req.Subdirectory == "" && req.Domain != "" {
		indexPath := filepath.Join(req.Docroot, "index.html")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			_ = writeDefaultIndex(ctx, indexPath, req.OSUser, req.Domain, req.Docroot)
		}
	}

	return dokuwikiDeleteResp{Status: "deleted"}, nil
}

func init() {
	RegisterAppDeleter("dokuwiki", dokuwikiDeleteHandler)
}
