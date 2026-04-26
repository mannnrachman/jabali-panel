package models

import "time"

// Panel certificate status state machine. See ADR-0066.
//
// self_signed → starting state and the fallback. The cert at
//   /etc/jabali/tls/panel.crt is the openssl-generated SAN cert
//   install.sh produced via provision_tls_cert.
// pending_acme → admin flipped use_le on (or M32 reconciler decided
//   to attempt because routable + use_le=1). Reconciler will dispatch
//   ssl.panel.issue on the next tick.
// issued → certbot returned a fresh lineage; deploy-hook copied
//   fullchain/privkey into /etc/jabali/tls/panel.{crt,key} and
//   reloaded nginx + jabali-panel + jabali-bulwark. expires_at
//   carries the LE notAfter.
// pending_acme_retry → certbot attempt failed once; last_error
//   carries the reason. Reconciler retries every 3h until either
//   issued or admin flips use_le off.
// failed → terminal flag for non-retryable errors (rate-limit
//   exhausted, hostname permanently unroutable). M32.1 will surface
//   how to clear this back to pending_acme.
const (
	PanelCertStatusSelfSigned       = "self_signed"
	PanelCertStatusPendingACME      = "pending_acme"
	PanelCertStatusIssued           = "issued"
	PanelCertStatusPendingACMERetry = "pending_acme_retry"
	PanelCertStatusFailed           = "failed"
)

// PanelCertificate is the singleton (id=1) row tracking the panel
// hostname's TLS cert lifecycle. Empty fields on first boot are
// seeded by the application; the migration only creates the table.
type PanelCertificate struct {
	ID            uint8      `gorm:"primaryKey;default:1"                              json:"id"`
	Hostname      string     `gorm:"type:varchar(253);not null;default:''"             json:"hostname"`
	Status        string     `gorm:"type:varchar(32);not null;default:'self_signed'"   json:"status"`
	CertPEMPath   string     `gorm:"type:varchar(255);not null;default:'/etc/jabali/tls/panel.crt'" json:"cert_pem_path"`
	IssuedAt      *time.Time `                                                         json:"issued_at,omitempty"`
	ExpiresAt     *time.Time `                                                         json:"expires_at,omitempty"`
	LastError     string     `gorm:"type:text"                                         json:"last_error,omitempty"`
	AttemptCount  uint32     `gorm:"type:int unsigned;not null;default:0"              json:"attempt_count"`
	NextRetryAt   *time.Time `                                                         json:"next_retry_at,omitempty"`
	Staging       bool       `gorm:"type:tinyint(1);not null;default:0"                json:"staging"`
	UseLE         bool       `gorm:"type:tinyint(1);not null;default:0"                json:"use_le"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime"                                    json:"updated_at"`
}

// TableName pins the GORM table name to the migration's spelling.
// Without this GORM would pluralise PanelCertificate to
// "panel_certificates", which doesn't exist.
func (PanelCertificate) TableName() string { return "panel_certificate" }
