// restore_forwarders.go — DA-specific email forwarder importer.
//
// DA stages /etc/virtual/<domain>/aliases under the cpmove tarball at
// etc/<domain>/aliases via the BackupUser script. This writer parses
// each file + inserts standalone EmailForwarder rows (MailboxID=NULL,
// ManagedBy="m35-da-import") for forwards that redirect to one or
// more targets.
//
// DA / exim alias file format:
//
//	local: target1, target2
//	another: |/path/to/program
//	# comment
//
// Lines starting with `:fail:`, `:defer:`, `|` (pipe to script), or
// `:include:` are operator-specific edge cases and skipped (logged
// as warnings). Plain mailbox-style targets persist as rows.
//
// Pure-redirect (no source mailbox) entries land as type='external'
// with MailboxID=NULL. The M65 mailbox-keyed reconciler ignores them
// (ListByMailboxID never matches NULL). A future domain-scoped
// reconciler phase will push them to Stalwart as Principal type=list.
package directadmin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// ForwardersResult summarises ImportForwarders' work.
type ForwardersResult struct {
	Inserted int      `json:"inserted"`
	Skipped  []string `json:"skipped,omitempty"`
}

// ImportForwarders walks <extractDir>/cpmove-<user>/etc/<domain>/
// aliases for every domain in domainsByName + inserts EmailForwarder
// rows. Returns counts of inserted forwarders + per-file warnings.
//
// Idempotent: existing (domain_id, local_part, target) triples skip
// rather than duplicating. Caller must have already inserted the
// domain rows (via cpanel.ImportDomains) so domainsByName resolves.
func ImportForwarders(
	ctx context.Context,
	fwdRepo repository.EmailForwarderRepository,
	domRepo repository.DomainRepository,
	extractDir string,
	sourceUser string,
) (*ForwardersResult, error) {
	res := &ForwardersResult{}
	// BackupUser wraps in cpmove-<user>/etc/<dom>/; also try unwrapped.
	candidates := []string{
		filepath.Join(extractDir, "cpmove-"+sourceUser, "etc"),
		filepath.Join(extractDir, "etc"),
	}
	var etcRoot string
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			etcRoot = c
			break
		}
	}
	if etcRoot == "" {
		res.Skipped = append(res.Skipped, "no_etc_dir_in_tarball")
		return res, nil
	}
	doms, err := os.ReadDir(etcRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", etcRoot, err)
	}
	for _, d := range doms {
		if !d.IsDir() {
			continue
		}
		domName := d.Name()
		dom, err := domRepo.FindByName(ctx, domName)
		if err != nil || dom == nil {
			res.Skipped = append(res.Skipped, "no_panel_domain:"+domName)
			continue
		}
		aliasFile := filepath.Join(etcRoot, domName, "aliases")
		body, err := os.ReadFile(aliasFile)
		if err != nil {
			// No aliases file → nothing to import; not an error.
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// `local: target1, target2`
			colon := strings.Index(line, ":")
			if colon < 1 {
				res.Skipped = append(res.Skipped, "malformed:"+aliasFile+":"+line)
				continue
			}
			local := strings.TrimSpace(line[:colon])
			rest := strings.TrimSpace(line[colon+1:])
			if local == "" || rest == "" {
				continue
			}
			// Skip exim-only directives operator-specific.
			if strings.HasPrefix(rest, "|") ||
				strings.HasPrefix(rest, ":fail:") ||
				strings.HasPrefix(rest, ":defer:") ||
				strings.HasPrefix(rest, ":include:") {
				res.Skipped = append(res.Skipped,
					fmt.Sprintf("exim_directive:%s@%s:%s", local, domName, rest))
				continue
			}
			for _, target := range strings.Split(rest, ",") {
				target = strings.TrimSpace(target)
				if target == "" {
					continue
				}
				// Insert one forwarder per target. MailboxID=NULL
				// because DA aliases are pure redirects with no
				// source mailbox; the future domain-scoped phase
				// will push these to Stalwart.
				lp := local
				f := &models.EmailForwarder{
					ID:        ids.NewULID(),
					MailboxID: nil,
					DomainID:  dom.ID,
					Type:      "external",
					LocalPart: &lp,
					Target:    target,
					Enabled:   true,
					ManagedBy: "m35-da-import",
				}
				if cErr := fwdRepo.Create(ctx, f); cErr != nil {
					// Most likely a unique-index conflict from a
					// previous re-run; log + skip.
					res.Skipped = append(res.Skipped,
						fmt.Sprintf("insert_failed:%s@%s→%s:%v",
							local, domName, target, cErr))
					continue
				}
				res.Inserted++
			}
		}
	}
	return res, nil
}
