// restore_extras.go — M35.8 P2/P5 "everything else" writers.
//
// Covered:
//   * Catch-all addresses    (cpanel: <home>/etc/<dom>/aliases line "*: target")
//   * Subdomains             (cpanel: SUB_DOMAINS=<sub.dom>,<sub.dom>,... in cp/<user>)
//
// Recorded-as-warning (no agent endpoint yet — operator follow-up):
//   * External forwarders    (M6.5 forwarders schema needs MailboxID; cpanel
//                             aliases can target external mail; not 1:1)
//   * Per-mailbox autoresponders (need post-mailbox-create id lookup)
//   * Sieve filters
//   * Per-domain custom SSL certs (LE auto-issue picks up once nginx vhost
//                                  exists; warning records source cert path)
//   * Per-domain PHP version  (needs per-domain pool create — deferred)
//   * FTP accounts            (deprecated in favour of SFTP keys)

package cpanel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// ExtrasResult is the per-area counter set returned from ImportExtras.
// Counters are summed into the parent restore manifest by the caller.
type ExtrasResult struct {
	CatchallsSet      int      // domains where catchall_target was written
	SubdomainsCreated int      // panel domain rows added from SUB_DOMAINS
	ForwardersLogged  int      // alias lines that would be forwarders if M6.5 schema allowed
	Skipped           []string // human-readable reasons (mirrors other restore writers)
}

// ImportExtras walks the parsed cpmove + reads cpanel meta files for
// the bits that don't have a dedicated restore_*.go writer yet.
// All operations are idempotent on rerun.
func ImportExtras(
	ctx context.Context,
	domainsRepo repository.DomainRepository,
	agentCli agent.AgentInterface,
	parsed *ParsedTarball,
	targetUserID, targetUsername string,
) (*ExtrasResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("ImportExtras: parsed nil")
	}
	res := &ExtrasResult{}

	// Discover the userdata file. PeekAccountMeta already searched
	// the same candidates; reuse its discovery to find the cpanel-
	// owner's primary-userdata file (cp/<user>) — that's where
	// SUB_DOMAINS lives.
	userdataPath := findUserdataFile(parsed.ExtractDir, parsed.SourceUser)

	// ---- P5: subdomains ----
	if userdataPath != "" {
		raw := extractKV(userdataPath, "SUB_DOMAINS")
		for _, name := range splitCpanelList(raw) {
			n := strings.ToLower(strings.TrimSpace(name))
			if n == "" {
				continue
			}
			if _, err := domainsRepo.FindByName(ctx, n); err == nil {
				res.Skipped = append(res.Skipped, "subdomain_skip:already_exists:"+n)
				continue
			}
			domainID := ids.NewULID()
			docRoot := filepath.Join("/home", targetUsername, "public_html", n)
			if _, err := agentCli.Call(ctx, "domain.create", map[string]any{
				"domain_id":      domainID,
				"domain":         n,
				"username":       targetUsername,
				"doc_root":       docRoot,
				"index_priority": "html_first",
			}); err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("subdomain_skip:agent_create:%s:%v", n, err))
				continue
			}
			now := time.Now()
			d := &models.Domain{
				ID:            domainID,
				UserID:        targetUserID,
				Name:          n,
				DocRoot:       docRoot,
				IsEnabled:     true,
				IndexPriority: "html_first",
				GhostState:    "unchecked",
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			if err := domainsRepo.Create(ctx, d); err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("subdomain_skip:db_create:%s:%v", n, err))
				continue
			}
			res.SubdomainsCreated++
		}
	}

	// ---- P2: catch-all from <homedir>/etc/<dom>/aliases ----
	// Format: lines like `address: target` or `*: target` (where
	// asterisk = the catch-all). We only consume the catch-all row;
	// forwarder rows are recorded as warnings (M6.5 schema needs a
	// MailboxID which we'd have to invent here).
	if parsed.HomeDir != "" {
		etcDir := filepath.Join(parsed.HomeDir, "etc")
		if doms, derr := os.ReadDir(etcDir); derr == nil {
			for _, d := range doms {
				if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
					continue
				}
				domName := d.Name()
				aliasFile := filepath.Join(etcDir, domName, "aliases")
				ct, fwd, sk := parseAliases(aliasFile)
				if ct != "" {
					dom, err := domainsRepo.FindByName(ctx, domName)
					if err != nil {
						res.Skipped = append(res.Skipped, fmt.Sprintf("catchall_skip:domain_missing:%s", domName))
					} else if uErr := domainsRepo.UpdateCatchallTarget(ctx, dom.ID, &ct); uErr != nil {
						res.Skipped = append(res.Skipped, fmt.Sprintf("catchall_skip:db_update:%s:%v", domName, uErr))
					} else {
						res.CatchallsSet++
					}
				}
				if fwd > 0 {
					res.ForwardersLogged += fwd
					res.Skipped = append(res.Skipped, fmt.Sprintf("forwarders_pending_manual:%s count=%d (M6.5 schema needs mailbox_id; restore via panel after manual mailbox setup)", domName, fwd))
				}
				res.Skipped = append(res.Skipped, sk...)
			}
		}
	}

	return res, nil
}

// parseAliases reads a cpanel-style aliases file. Returns
// (catchAllTarget, forwarderCount, perLineWarnings). Comment lines
// (`#…`) and blank lines are skipped; lines without `:` are skipped.
func parseAliases(path string) (string, int, []string) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, nil
	}
	defer f.Close()

	var catchAll string
	var fwdCount int
	var warnings []string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Cpanel default-mailbox-handling lines look like
		//   :defaultaddress: :fail: No Such User Here
		// or
		//   :fail: No Such User Here
		// Ignore everything that doesn't have the simple "key: value" shape.
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		if key == "*" {
			// Drop quoting if present.
			catchAll = strings.Trim(val, `"'`)
			continue
		}
		// Anything else is a per-address forwarder — record-only for now.
		_ = key
		_ = val
		fwdCount++
	}
	if err := sc.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("aliases_scan_err:%s:%v", path, err))
	}
	return catchAll, fwdCount, warnings
}

// findUserdataFile probes the same locations PeekAccountMeta uses
// for the cp/<user> file. Returns the first hit or "".
func findUserdataFile(extractDir, sourceUser string) string {
	candidates := []string{
		filepath.Join(extractDir, "cpmove-"+sourceUser, "cp", sourceUser),
		filepath.Join(extractDir, "cp", sourceUser),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// splitCpanelList splits comma-separated lists out of cpanel's
// `KEY=v1,v2,v3` userdata lines. Tolerant of stray whitespace.
func splitCpanelList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
