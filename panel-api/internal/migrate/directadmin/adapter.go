package directadmin

import (
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/migrate/cpanel"
)

// ToCpanelParsed produces a *cpanel.ParsedTarball view over a
// DAParsedTarball so the cpanel restore writers (ImportDatabases,
// ImportSSHKeys, ImportCron) run unchanged against DA-extracted
// content. Per-area mapping:
//
//   DAParsedTarball.MySQLDumps     → cpanel.ParsedTarball.MySQLDumps
//   DAParsedTarball.SSHAuthorized  → cpanel.ParsedTarball.SSHAuthorized (wrap to list)
//   DAParsedTarball.CronFile       → cpanel.ParsedTarball.CronFiles (wrap to list)
//   DAParsedTarball.HomeDir        → cpanel.ParsedTarball.HomeDir
//   DAParsedTarball.SourceUser     → cpanel.ParsedTarball.SourceUser
//
// Fields cpanel writers care about that DA doesn't populate:
//   - ZoneFiles: DA backup tar doesn't contain BIND zones; DNS
//     migration is operator-driven (run dns.zone.upsert manually
//     against parsed `da admin user.show -d <dom>` output).
//
// ImportHome (cpanel rsync) still needs an adapter wrapper because
// DA's homedir layout differs (<user>/domains/<dom> vs cpanel's
// <user>/public_html). That adapter ships in a follow-up — for v1
// the operator hand-rsyncs the DA domains/ subtree.
func ToCpanelParsed(da *DAParsedTarball) *cpanel.ParsedTarball {
	if da == nil {
		return nil
	}
	out := &cpanel.ParsedTarball{
		SourceUser: da.SourceUser,
		ExtractDir: da.ExtractDir,
		HomeDir:    da.HomeDir,
		MailRoot:   da.MailRoot, // DA stores at <user>/email/, cpanel at <user>/mail/
		MySQLDumps: da.MySQLDumps,
		Skipped:    da.Skipped,
	}
	if da.SSHAuthorized != "" {
		out.SSHAuthorized = []string{da.SSHAuthorized}
	}
	if da.CronFile != "" {
		out.CronFiles = []string{da.CronFile}
	}
	return out
}
