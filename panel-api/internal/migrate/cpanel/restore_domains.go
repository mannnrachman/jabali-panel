package cpanel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DomainImportResult is returned to the restore-stage caller for
// progress reporting + manifest update.
type DomainImportResult struct {
	Created      int
	EmailEnabled int
	Skipped      []string
}

type domainEmailEnableResp struct {
	Ok            bool   `json:"ok"`
	DKIMSelector  string `json:"dkim_selector"`
	DKIMPublicKey string `json:"dkim_public_key"`
}

// ImportDomains creates panel domain rows + nginx vhosts for every
// zone file found in the parsed tarball. If a domain's mail directory
// exists under the tarball's MailRoot, email is enabled on the spot so
// the ImportMailboxes stage that follows can deliver into Stalwart.
//
// Idempotent: existing domains (matched by name) are skipped rather
// than duplicated.
func ImportDomains(
	ctx context.Context,
	domainsRepo repository.DomainRepository,
	agentCli agent.AgentInterface,
	parsed *ParsedTarball,
	targetUserID string,
	targetUsername string,
) (*DomainImportResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("ImportDomains: parsed nil")
	}
	res := &DomainImportResult{}
	mailDomains := scanMailDomains(parsed)
	// jabali convention: every domain docroot lives at
	//   /home/<user>/domains/<dom>/public_html/
	// regardless of source layout. M35.8 ImportHomeSplit rsyncs
	// per-domain content into that target, so the nginx vhost +
	// FPM pool point at the right path. Source-layout-specific
	// overrides (DA already-resolved DocRoots) still win when set.
	docRootFor := func(name string) string {
		if parsed.DocRoots != nil {
			if override, ok := parsed.DocRoots[name]; ok && override != "" {
				return override
			}
		}
		return filepath.Join("/home", targetUsername, "domains", name, "public_html")
	}

	type domainEntry struct {
		name    string
		docRoot string
	}
	var entries []domainEntry
	if len(parsed.ZoneFiles) > 0 {
		for _, zonePath := range parsed.ZoneFiles {
			domainName := strings.TrimSuffix(filepath.Base(zonePath), ".db")
			if domainName == "" {
				res.Skipped = append(res.Skipped, fmt.Sprintf("domain_skip:empty_name_from_%s", zonePath))
				continue
			}
			entries = append(entries, domainEntry{
				name:    domainName,
				docRoot: docRootFor(domainName),
			})
		}
	} else {
		for _, name := range parsed.DomainNames {
			if name == "" {
				continue
			}
			entries = append(entries, domainEntry{name: name, docRoot: docRootFor(name)})
		}
	}

	for _, e := range entries {
		domainName := e.name
		if _, err := domainsRepo.FindByName(ctx, domainName); err == nil {
			res.Skipped = append(res.Skipped, "domain_skip:already_exists:"+domainName)
			continue
		}

		domainID := ids.NewULID()
		docRoot := e.docRoot

		if _, err := agentCli.Call(ctx, "domain.create", map[string]any{
			"domain_id":      domainID,
			"domain":         domainName,
			"username":       targetUsername,
			"doc_root":       docRoot,
			"index_priority": "html_first",
		}); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("domain_skip:agent_create_failed:%s:%v", domainName, err))
			continue
		}

		now := time.Now()
		d := &models.Domain{
			ID:            domainID,
			UserID:        targetUserID,
			Name:          domainName,
			DocRoot:       docRoot,
			IsEnabled:     true,
			IndexPriority: "html_first",
			GhostState:    "unchecked",
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := domainsRepo.Create(ctx, d); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("domain_skip:db_create_failed:%s:%v", domainName, err))
			continue
		}
		res.Created++

		if !mailDomains[domainName] {
			continue
		}

		raw, err := agentCli.Call(ctx, "domain.email_enable", map[string]any{
			"domain_id":   domainID,
			"domain_name": domainName,
		})
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("email_skip:enable_failed:%s:%v", domainName, err))
			continue
		}
		var emailResp domainEmailEnableResp
		if err := json.Unmarshal(raw, &emailResp); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("email_skip:decode_failed:%s:%v", domainName, err))
			continue
		}

		enabledAt := time.Now()
		if err := domainsRepo.UpdateEmailState(ctx, domainID, repository.DomainEmailState{
			Enabled:        true,
			DkimSelector:   &emailResp.DKIMSelector,
			DkimPublicKey:  &emailResp.DKIMPublicKey,
			EmailEnabledAt: &enabledAt,
		}); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("email_skip:db_update_failed:%s:%v", domainName, err))
			continue
		}
		res.EmailEnabled++
	}
	return res, nil
}

// scanMailDomains returns the set of domain names that have a mail
// directory under the tarball's MailRoot (i.e. domains that had
// mailboxes in the source panel).
func scanMailDomains(parsed *ParsedTarball) map[string]bool {
	result := make(map[string]bool)
	mailRoot := parsed.MailRoot
	if mailRoot == "" && parsed.HomeDir != "" {
		mailRoot = filepath.Join(parsed.HomeDir, "mail")
	}
	if mailRoot == "" {
		return result
	}
	entries, err := os.ReadDir(mailRoot)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			result[e.Name()] = true
		}
	}
	return result
}
