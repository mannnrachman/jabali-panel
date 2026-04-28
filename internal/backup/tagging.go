// Package backup is the M30 wave-gate contract: types + restic CLI
// wrapper + manifest schema + tagging convention. Steps 3-12 (agent
// serializers, orchestrator, restore worker, REST handlers, system
// backup/restore) all build against this package.
//
// See ADR-0075 + plans/m30-backup-restore.md.
package backup

import "fmt"

// Tag is one snapshot tag. Tags are key=value or bare strings; the
// blanket `jabali` tag has no value.
type Tag string

// Tag keys (left-hand side of `key=value`). Restic tags themselves
// are opaque strings; we constrain the ones Jabali emits so retention
// + listing can filter reliably.
const (
	TagKeyKind   = "kind"
	TagKeyJobID  = "job-id"
	TagKeyStage  = "stage"
	TagKeyUserID = "user-id"
	TagKeySystem = "system"
	TagKeyDB     = "db"
)

// Blanket tag applied to every Jabali-managed snapshot. Retention
// commands scope on this tag so foreign restic snapshots (operators
// running their own backups in the same repo) are never pruned.
const TagBlanket Tag = "jabali"

// BackupKind values — match BackupJobKind constants in panel-api/models.
const (
	KindAccountBackup  = "account_backup"
	KindAccountRestore = "account_restore"
	KindSystemBackup   = "system_backup"
	KindSystemRestore  = "system_restore"
)

// Stage values — ENUM-like at the wire, free-form at the schema. Each
// stage produces one restic snapshot; the manifest stage stitches them
// back together at restore.
const (
	// account-backup stages
	StageHome     = "home"
	StageDB       = "db"
	StageMail     = "mail"
	StageDNS      = "dns"
	StageCron     = "cron"
	StageSSH      = "ssh"
	StageApps     = "apps"
	StagePHP      = "php"
	StageMeta     = "meta" // YAML bundle for cron/ssh/apps/php
	StageManifest = "manifest"

	// system-backup stages
	StagePanelDB       = "panel_db"
	StagePanelConfig   = "panel_config"
	StageServiceConfig = "service_config"
	StageMailState     = "mail_state"
	StageTLS           = "tls"
	StageSecurity      = "security"
	StageOSUsers       = "os_users"
	// Disaster-recovery stages — enough state to rebuild a host from
	// a fresh Debian install + jabali-panel reinstall + this backup.
	StageOSBase    = "os_base"    // hostname, hosts, fstab, netplan, sysctl
	StageAPT       = "apt"        // sources.list*, keyrings, prefs
	StageSSHHost   = "ssh_host"   // sshd_config(.d) + host keys
	StageSystemCron = "system_cron" // /etc/cron.d/*, /var/spool/cron/crontabs
	StageDataState = "data_state" // redis, crowdsec, pdns runtime data
	StageSudoers   = "sudoers"    // /etc/sudoers + sudoers.d
)

// MakeTag formats `key=value`.
func MakeTag(key, value string) Tag {
	return Tag(fmt.Sprintf("%s=%s", key, value))
}

// AccountBackupTags returns the canonical tag set for one stage of
// an account_backup. Order is irrelevant to restic but stable here
// for golden-file tests.
func AccountBackupTags(jobID, userID, stage string) []Tag {
	return []Tag{
		TagBlanket,
		MakeTag(TagKeyKind, KindAccountBackup),
		MakeTag(TagKeyJobID, jobID),
		MakeTag(TagKeyUserID, userID),
		MakeTag(TagKeyStage, stage),
	}
}

// SystemBackupTags returns the canonical tag set for one stage of
// a system_backup. `system=<hostname>` lets multi-host operators
// disambiguate snapshots in a shared remote repo.
func SystemBackupTags(jobID, hostname, stage string) []Tag {
	return []Tag{
		TagBlanket,
		MakeTag(TagKeyKind, KindSystemBackup),
		MakeTag(TagKeyJobID, jobID),
		MakeTag(TagKeySystem, hostname),
		MakeTag(TagKeyStage, stage),
	}
}

// ToStrings converts a tag slice to []string for restic CLI args.
func ToStrings(tags []Tag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = string(t)
	}
	return out
}
