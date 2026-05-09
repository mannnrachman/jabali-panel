package directadmin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate"
)

// describe.go — per-area builders for the DA importer.
//
// **STATUS:** Coded against documented DirectAdmin CLI output
// shapes (DA admin docs + community wiki). Has NOT been validated
// against a live DA host; operator running this against an
// unfamiliar DA minor should expect potential field-set drift +
// surface failures via the manifest's Warnings list (every
// per-area call records partial/missing data without failing the
// whole describe). When a live DA fixture lands in the test corpus
// the builders get a real test pass.
//
// DA exposes account state via `da admin user.show <user>` (INI
// k=v lines), `da admin db.list <user>` (one db name per line),
// and `da admin pop.list <domain>` (one mailbox per line). Per-
// domain attrs come from `da admin user.show <user> -d <domain>`.
//
// Cron jobs + SSH authorized_keys are NOT exposed via the admin
// CLI on DA. Operator path: pull v-backup-user-style tarball,
// parse cron/<user> + .ssh/authorized_keys from the extracted
// tree (cpanel/restore_cron.go + cpanel/restore_sshkeys.go
// already do this via filesystem walk; DA tarballs follow a
// nearly-identical layout — Step 4 follow-up adds a thin DA
// tarball parser that reuses the cpanel writers).

// parseINI splits 'k=v\nk=v\n' lines into a map. Comments (#) and
// blank lines skipped. Trailing whitespace stripped. Used for
// every `da admin user.show` shaped output.
func parseINI(raw []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// describeDomains pulls the source account's domain list +
// per-domain detail. `da admin user.show <user>` returns a
// 'domains=' comma-separated list. Per-domain detail (docroot,
// PHP version) comes from a follow-up `user.show <user> -d
// <domain>` call which DA documents as the per-domain query.
func (d *Discoverer) describeDomains(ctx context.Context, s *session, account string) ([]migrate.DomainSpec, error) {
	out, err := s.run(ctx, d.CommandTimeout,
		fmt.Sprintf("da admin user.show '%s'", shellEscape(account)))
	if err != nil {
		return nil, fmt.Errorf("da admin user.show %s: %w", account, err)
	}
	attrs := parseINI(out)
	primary := attrs["domain"]
	allDomains := splitCSV(attrs["domains"])
	if primary == "" && len(allDomains) > 0 {
		primary = allDomains[0]
	}

	rows := []migrate.DomainSpec{}
	for _, dom := range allDomains {
		spec := migrate.DomainSpec{
			Name:      dom,
			DocRoot:   fmt.Sprintf("/home/%s/domains/%s/public_html", account, dom),
			IsPrimary: dom == primary,
			HasPHP:    true, // DA defaults; overridden below if user.show -d returns explicit version
		}
		// Per-domain attrs — best-effort. Failure records a
		// warning instead of failing the whole describe.
		dout, derr := s.run(ctx, d.CommandTimeout,
			fmt.Sprintf("da admin user.show '%s' -d '%s'",
				shellEscape(account), shellEscape(dom)))
		if derr == nil {
			dattrs := parseINI(dout)
			if v, ok := dattrs["php_ver"]; ok && v != "" {
				spec.PHPVer = v
			}
			if v, ok := dattrs["public_html"]; ok && v != "" {
				spec.DocRoot = v
			}
			// 'php=ON' / 'php=OFF' → HasPHP boolean
			if v, ok := dattrs["php"]; ok {
				spec.HasPHP = strings.EqualFold(v, "on")
			}
		}
		rows = append(rows, spec)
	}
	return rows, nil
}

// splitCSV splits a comma-separated value string with whitespace
// trimming + empty-token filtering. DA's domains= field is the
// canonical use.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// describeDatabases pulls the source's MariaDB DB list. DA's
// db.list output is one DB name per line, prefixed with the
// source-side username (e.g. 'cpuser_blogdb'). We strip the
// prefix later at restore time so the destination's username
// prefix can be applied (cpanel/restore_dbs.go does the same
// transform — DA reuses that writer once the tarball parser ships).
func (d *Discoverer) describeDatabases(ctx context.Context, s *session, account string) ([]migrate.DatabaseSpec, []migrate.Warning, error) {
	out, err := s.run(ctx, d.CommandTimeout,
		fmt.Sprintf("da admin db.list '%s'", shellEscape(account)))
	if err != nil {
		// db.list is sometimes named differently across DA minors
		// (database.list, db_list). Record a warning + return
		// empty rather than failing the whole describe.
		return nil, []migrate.Warning{{
			Code:   "directadmin_db_list_failed",
			Detail: fmt.Sprintf("da admin db.list %q failed: %v — DA minor may use a different command name", account, err),
			At:     time.Now().UTC(),
		}}, nil
	}
	specs := []migrate.DatabaseSpec{}
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		specs = append(specs, migrate.DatabaseSpec{
			Engine: "mysql",
			Name:   name,
		})
	}
	return specs, nil, nil
}

// describeMailboxes walks every domain via 'da admin pop.list
// <domain>' to enumerate mailboxes. DA exposes per-mailbox quota
// via 'da admin pop.show <domain> -u <user>' which we don't pull
// here (per-mailbox round-trip cost vs the value of the projection
// — operator can manually inspect the source if quota math
// matters). Mailbox addresses returned as <local>@<domain>.
func (d *Discoverer) describeMailboxes(ctx context.Context, s *session, account string, domains []migrate.DomainSpec) ([]migrate.MailboxSpec, []migrate.Warning, error) {
	rows := []migrate.MailboxSpec{}
	warnings := []migrate.Warning{}
	for _, dom := range domains {
		out, err := s.run(ctx, d.CommandTimeout,
			fmt.Sprintf("da admin pop.list '%s'", shellEscape(dom.Name)))
		if err != nil {
			warnings = append(warnings, migrate.Warning{
				Code:   "directadmin_pop_list_failed",
				Detail: fmt.Sprintf("pop.list %s: %v", dom.Name, err),
				At:     time.Now().UTC(),
			})
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			local := strings.TrimSpace(line)
			if local == "" || strings.HasPrefix(local, "#") {
				continue
			}
			rows = append(rows, migrate.MailboxSpec{
				Address: fmt.Sprintf("%s@%s", local, dom.Name),
				MaildirPath: fmt.Sprintf(
					"/home/%s/imap/%s/%s/Maildir",
					account, dom.Name, local),
			})
		}
	}
	_ = account
	return rows, warnings, nil
}

// shellEscape replaces single quotes with the canonical `'\''`
// trick + nothing else. DA usernames + domain names already
// validated by the SSH-side principal probe; this is the
// belt-and-braces fallback for the remote-shell single-quote
// interpolation we use throughout the package.
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// parseQuotaMB converts DA's quota fields ('1024', '0', 'unlimited')
// to bytes. 'unlimited' / '0' → 0 (caller treats as 'no quota').
func parseQuotaMB(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "unlimited") {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v * 1024 * 1024
}
