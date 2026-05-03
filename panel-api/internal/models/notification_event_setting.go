package models

import "time"

// NotificationEventSetting is a per-event-kind enable toggle. The
// dispatcher consults this table on every Publish and skips
// envelopes whose event_kind is disabled. Operators edit values
// from the admin Notifications → Events tab.
type NotificationEventSetting struct {
	EventKind string    `gorm:"column:event_kind;primaryKey;type:varchar(60)" json:"event_kind"`
	Enabled   bool      `gorm:"column:enabled;type:tinyint(1);not null;default:0" json:"enabled"`
	UpdatedAt time.Time `gorm:"column:updated_at;type:datetime(6);not null" json:"updated_at"`
}

func (NotificationEventSetting) TableName() string { return "notification_event_settings" }

// NotificationEventKindMeta holds the static metadata for every
// event kind the dispatcher knows about — display label, short
// description, default-on flag, and severity colour. Used for both
// the EnsureDefaults seed and the admin UI list endpoint.
type NotificationEventKindMeta struct {
	Kind        string
	Label       string
	Description string
	Severity    string // info | warning | error | critical
	DefaultOn   bool
}

// AllNotificationEventKinds — the canonical, ordered list. Add new
// kinds here AND wire a publisher that fires them; the seed picks
// up new kinds with their declared default on next boot.
var AllNotificationEventKinds = []NotificationEventKindMeta{
	{
		Kind:        "cert.renew.fail",
		Label:       "SSL renewal failed",
		Description: "ACME renewal failed — issued via panel certbot. Action usually required.",
		Severity:    "error",
		DefaultOn:   true,
	},
	{
		Kind:        "cert.renew.ok",
		Label:       "SSL renewal succeeded",
		Description: "ACME renewal completed. Informational confirmation.",
		Severity:    "info",
		DefaultOn:   false,
	},
	{
		Kind:        "domain.expiry.7d",
		Label:       "SSL cert expiring in 7 days",
		Description: "Cert crosses the 7-day pre-expiry window without a fresh renewal.",
		Severity:    "warning",
		DefaultOn:   true,
	},
	{
		Kind:        "domain.expiry.1d",
		Label:       "SSL cert expiring in 1 day",
		Description: "Cert crosses the 1-day pre-expiry window — imminent expiry.",
		Severity:    "error",
		DefaultOn:   true,
	},
	{
		Kind:        "disk.full.warn",
		Label:       "Disk usage 80%",
		Description: "System disk passed the 80% threshold. Cleanup recommended.",
		Severity:    "warning",
		DefaultOn:   false,
	},
	{
		Kind:        "disk.full.crit",
		Label:       "Disk usage 95%",
		Description: "System disk passed the 95% threshold — services will fail soon.",
		Severity:    "critical",
		DefaultOn:   true,
	},
	{
		Kind:        "disk.quota.warn",
		Label:       "User reached 90% quota",
		Description: "A hosting user crossed 90% of their disk quota.",
		Severity:    "warning",
		DefaultOn:   true,
	},
	{
		Kind:        "load.high",
		Label:       "High server load",
		Description: "1-minute load average exceeded the configured threshold.",
		Severity:    "warning",
		DefaultOn:   false,
	},
	{
		Kind:        "system.update.available",
		Label:       "System updates available",
		Description: "apt has new package upgrades waiting (security or otherwise).",
		Severity:    "info",
		DefaultOn:   false,
	},
	{
		Kind:        "service.down",
		Label:       "Service down or restarted",
		Description: "A managed service unit (nginx, php-fpm, mariadb, …) failed or was restarted.",
		Severity:    "error",
		DefaultOn:   true,
	},
	{
		Kind:        "crowdsec.ban.spike",
		Label:       "CrowdSec ban spike",
		Description: "Unusually large burst of new bans within a short window.",
		Severity:    "warning",
		DefaultOn:   false,
	},
	{
		Kind:        "backup.fail",
		Label:       "Backup failed",
		Description: "A scheduled backup job exited non-zero.",
		Severity:    "error",
		DefaultOn:   true,
	},
	{
		Kind:        "admin.login",
		Label:       "Admin signed in",
		Description: "First request of a new Kratos admin session.",
		Severity:    "info",
		DefaultOn:   true,
	},
	{
		Kind:        "ssh.login",
		Label:       "SSH login",
		Description: "Successful SSH authentication for a panel-managed user.",
		Severity:    "info",
		DefaultOn:   true,
	},
	{
		Kind:        "notifications.channel.auto_disabled",
		Label:       "Channel auto-disabled",
		Description: "A notification channel was disabled after repeated send failures.",
		Severity:    "warning",
		DefaultOn:   true,
	},
	{
		Kind:        "panel.welcome",
		Label:       "Welcome",
		Description: "Fired once at install time for the bootstrap admin to point them at notification setup.",
		Severity:    "info",
		DefaultOn:   true,
	},
	{
		Kind:        "security.decision.fired",
		Label:       "Security decision fired",
		Description: "Aggregated drop/throttle from any decision brain (UFW, nginx limit_req, CrowdSec). One envelope per polling window when activity exceeds 0.",
		Severity:    "info",
		DefaultOn:   false,
	},
	{
		Kind:        "snuffleupagus.incident.detected",
		Label:       "PHP Defense incident",
		Description: "PHP Defense rule fired on a tenant request (block / simulated block / log).",
		Severity:    "warning",
		DefaultOn:   true,
	},
}

// LookupNotificationEventKind returns the metadata for a known kind
// or nil if unknown. Used by handlers + dispatcher.
func LookupNotificationEventKind(kind string) *NotificationEventKindMeta {
	for i := range AllNotificationEventKinds {
		if AllNotificationEventKinds[i].Kind == kind {
			return &AllNotificationEventKinds[i]
		}
	}
	return nil
}
