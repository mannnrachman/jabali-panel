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
	CatchallsSet          int      // domains where catchall_target was written
	SubdomainsCreated     int      // panel domain rows added from SUB_DOMAINS
	ForwardersCreated     int      // mail forwarder rows added (M6.5)
	ForwardersOrphaned    int      // alias lines whose local mailbox isn't in panel
	AutorespondersCreated int      // vacation auto-replies restored (M6.5)
	AutorespondersOrphaned int     // .autorespond entries whose mailbox isn't in panel
	PHPVersionApplied     string   // jabali version chosen for the user pool ("" = skipped)
	FTPAccountsObserved   int      // cpanel ftp accounts seen on source (record-only)
	Skipped               []string // human-readable reasons (mirrors other restore writers)
}

// ImportExtras walks the parsed cpmove + reads cpanel meta files for
// the bits that don't have a dedicated restore_*.go writer yet.
// All operations are idempotent on rerun.
func ImportExtras(
	ctx context.Context,
	domainsRepo repository.DomainRepository,
	mailboxesRepo repository.MailboxRepository,
	forwardersRepo repository.EmailForwarderRepository,
	autoRespondersRepo repository.EmailAutoresponderRepository,
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
				ct, fwds, sk := parseAliases(aliasFile)
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
				// Forwarder rows — M6.5 EmailForwarder needs both
				// MailboxID + DomainID. Look up by `<local>@<dom>` to
				// find the source mailbox; skip the line when no
				// matching panel mailbox exists (cpanel allows forwards
				// without a local mailbox, jabali doesn't).
				for _, f := range fwds {
					if mailboxesRepo == nil || forwardersRepo == nil {
						res.Skipped = append(res.Skipped, "forwarders_skip:repos_unwired")
						break
					}
					local := f.Local
					target := f.Target
					if local == "" || target == "" {
						continue
					}
					srcEmail := local + "@" + domName
					mb, mErr := mailboxesRepo.FindByEmail(ctx, srcEmail)
					if mErr != nil || mb == nil {
						res.ForwardersOrphaned++
						res.Skipped = append(res.Skipped, fmt.Sprintf("forwarder_orphan:%s→%s (no local mailbox)", srcEmail, target))
						continue
					}
					fwd := &models.EmailForwarder{
						ID:        ids.NewULID(),
						MailboxID: mb.ID,
						DomainID:  mb.DomainID,
						Type:      "external",
						LocalPart: &local,
						Target:    target,
						Enabled:   true,
						ManagedBy: "m35",
					}
					if cErr := forwardersRepo.Create(ctx, fwd); cErr != nil {
						res.Skipped = append(res.Skipped, fmt.Sprintf("forwarder_skip:create:%s→%s:%v", srcEmail, target, cErr))
						continue
					}
					res.ForwardersCreated++
				}
				res.Skipped = append(res.Skipped, sk...)
			}
		}
	}

	// ---- P4: user-wide PHP version ----
	// cpanel writes per-domain `<dom>.php-fpm.yaml` files containing
	// `phpversion: ea-php82`. Jabali stores a single FPM pool per
	// user (not per-domain), so we pick the source's primary-domain
	// version + apply it via php.pool.apply. Other domains' versions
	// land as warnings + the operator can fix post-migration.
	if agentCli != nil {
		if ver, mixed := detectPHPVersion(parsed); ver != "" {
			_, err := agentCli.Call(ctx, "php.pool.apply", map[string]any{
				"username":        targetUsername,
				"php_version":     ver,
				"pm_max_children": uint32(20), // jabali default; pool.apply clamps
			})
			if err != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("php_skip:%s:%v", ver, err))
			} else {
				res.PHPVersionApplied = ver
			}
			if len(mixed) > 0 {
				res.Skipped = append(res.Skipped, fmt.Sprintf("php_mixed_versions:source had %v; using %s for user pool", mixed, ver))
			}
		}
	}

	// ---- P6: FTP accounts (record-only) ----
	if parsed.HomeDir != "" {
		if files, derr := os.ReadDir(filepath.Join(parsed.HomeDir, "etc")); derr == nil {
			for _, f := range files {
				if !f.IsDir() {
					continue
				}
				ftpPasswd := filepath.Join(parsed.HomeDir, "etc", f.Name(), "passwd")
				if n := countNonCommentLines(ftpPasswd); n > 0 {
					res.FTPAccountsObserved += n
					res.Skipped = append(res.Skipped, fmt.Sprintf("ftp_observed:%s count=%d (FTP deprecated — re-issue via SFTP keys)", f.Name(), n))
				}
			}
		}
	}

	// ---- P2.5: per-mailbox autoresponders ----
	// cpanel layout: <homedir>/.autorespond/<address>.{conf,yaml}
	if parsed.HomeDir != "" && autoRespondersRepo != nil && mailboxesRepo != nil {
		respDir := filepath.Join(parsed.HomeDir, ".autorespond")
		if files, derr := os.ReadDir(respDir); derr == nil {
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				name := f.Name()
				// strip .conf / .yaml — leaving the address
				addr := strings.TrimSuffix(strings.TrimSuffix(name, ".conf"), ".yaml")
				if !strings.Contains(addr, "@") {
					continue
				}
				mb, mErr := mailboxesRepo.FindByEmail(ctx, addr)
				if mErr != nil || mb == nil {
					res.AutorespondersOrphaned++
					res.Skipped = append(res.Skipped, fmt.Sprintf("autoresponder_orphan:%s (no local mailbox)", addr))
					continue
				}
				ar := parseAutoresponder(filepath.Join(respDir, name), mb.ID)
				if ar == nil {
					res.Skipped = append(res.Skipped, fmt.Sprintf("autoresponder_skip:%s parse failed", addr))
					continue
				}
				if uErr := autoRespondersRepo.Update(ctx, ar); uErr != nil {
					res.Skipped = append(res.Skipped, fmt.Sprintf("autoresponder_skip:db:%s:%v", addr, uErr))
					continue
				}
				res.AutorespondersCreated++
			}
		}
	}

	return res, nil
}

// parseAutoresponder reads a cpanel .autorespond/<addr>.{conf,yaml}
// file + builds an EmailAutoresponder row. Both formats share the
// same "Header: value" / blank line / body shape; YAML adds quoting
// which we strip.
func parseAutoresponder(path, mailboxID string) *models.EmailAutoresponder {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(string(raw), "\n\n", 2)
	headerBlock := parts[0]
	var bodyPart string
	if len(parts) == 2 {
		bodyPart = parts[1]
	}
	var subject string
	for _, line := range strings.Split(headerBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
		if strings.EqualFold(k, "subject") {
			subject = v
		}
	}
	body := strings.TrimSpace(bodyPart)
	if body == "" && subject == "" {
		return nil
	}
	ar := &models.EmailAutoresponder{
		MailboxID: mailboxID,
		Enabled:   true,
		ManagedBy: "m35",
	}
	if subject != "" {
		ar.Subject = &subject
	}
	if body != "" {
		ar.TextBody = &body
	}
	return ar
}

// aliasForward is one forwarder row out of an aliases file:
// `<local>@<domain>: <target>` → Local="<local>", Target="<target>".
type aliasForward struct {
	Local  string
	Target string
}

// parseAliases reads a cpanel-style aliases file. Returns
// (catchAllTarget, forwarderRows, perLineWarnings).
func parseAliases(path string) (string, []aliasForward, []string) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, nil
	}
	defer f.Close()

	var catchAll string
	var forwards []aliasForward
	var warnings []string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Skip cpanel meta-lines that begin with `:` (e.g. ":fail:").
		if strings.HasPrefix(line, ":") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
		if key == "*" {
			catchAll = val
			continue
		}
		// `key` may be `local` or `local@domain`. Normalise to local.
		if at := strings.IndexByte(key, '@'); at > 0 {
			key = key[:at]
		}
		forwards = append(forwards, aliasForward{Local: key, Target: val})
	}
	if err := sc.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("aliases_scan_err:%s:%v", path, err))
	}
	return catchAll, forwards, warnings
}

// detectPHPVersion scans <userdata>/*.php-fpm.yaml files for the
// `phpversion: ea-php82` line + returns (primaryVersion, otherVersions).
// "primary" = the most-frequent version (operator can fix outliers
// after migration). Empty string when no .php-fpm.yaml found.
func detectPHPVersion(parsed *ParsedTarball) (string, []string) {
	// userdata dir lives next to the cp/<user> file inside the
	// extracted wrapper. Same probe set PeekAccountMeta uses.
	roots := []string{
		filepath.Join(parsed.ExtractDir, "cpmove-"+parsed.SourceUser, "userdata"),
		filepath.Join(parsed.ExtractDir, "userdata"),
		filepath.Join(parsed.ExtractDir, "cp", parsed.SourceUser, "userdata"),
	}
	versions := map[string]int{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".php-fpm.yaml") {
				continue
			}
			if v := normalisePHPVersion(extractKV(filepath.Join(root, e.Name()), "phpversion")); v != "" {
				versions[v]++
			}
		}
		if len(versions) > 0 {
			break
		}
	}
	if len(versions) == 0 {
		return "", nil
	}
	// Most-frequent wins; ties pick lexicographically lower so reruns
	// are deterministic.
	primary := ""
	bestCount := 0
	for v, c := range versions {
		if c > bestCount || (c == bestCount && (primary == "" || v < primary)) {
			primary = v
			bestCount = c
		}
	}
	var others []string
	for v := range versions {
		if v != primary {
			others = append(others, v)
		}
	}
	return primary, others
}

// normalisePHPVersion maps cpanel's "ea-php82" / "ea-php-rpm-7.4" /
// raw "8.2" strings to jabali's "<major>.<minor>" form so
// php.pool.apply accepts them. Empty string on unparseable input.
func normalisePHPVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "ea-php")
	raw = strings.TrimPrefix(raw, "ea-")
	raw = strings.TrimPrefix(raw, "php-")
	raw = strings.TrimPrefix(raw, "php")
	raw = strings.TrimPrefix(raw, "-rpm-")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// "82" → "8.2"; already-dotted "8.2" passes through.
	if !strings.Contains(raw, ".") && len(raw) == 2 {
		return string(raw[0]) + "." + string(raw[1])
	}
	return raw
}

// countNonCommentLines tallies non-blank, non-#-prefixed lines in
// a file. Used for FTP-account counting where the source's
// /etc/<dom>/passwd is a colon-separated user list.
func countNonCommentLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
	}
	return n
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
