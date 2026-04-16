package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainListResponse is the output shape for domain.list.
type domainListResponse struct {
	Sites []string `json:"sites"`
}

func domainListHandler(ctx context.Context, params json.RawMessage) (any, error) {
	// domain.list takes no parameters
	sitesEnabledDir := "/etc/nginx/sites-enabled"

	// Read the sites-enabled directory
	entries, err := os.ReadDir(sitesEnabledDir)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to read sites-enabled directory: %v", err),
		}
	}

	var sites []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Strip .conf suffix if present
		if strings.HasSuffix(name, ".conf") {
			name = strings.TrimSuffix(name, ".conf")
		}

		sites = append(sites, name)
	}

	return domainListResponse{
		Sites: sites,
	}, nil
}

func init() {
	Default.Register("domain.list", domainListHandler)
}
