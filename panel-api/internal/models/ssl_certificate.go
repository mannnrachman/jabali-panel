package models

import "time"

// SSL certificate status constants.
const (
	SSLStatusPending  = "pending"
	SSLStatusIssuing  = "issuing"
	SSLStatusIssued   = "issued"
	SSLStatusFailed   = "failed"
	SSLStatusRevoked  = "revoked"
	SSLStatusRenewing = "renewing"
)

// SSLCertificate represents a managed SSL/TLS certificate for a hosted domain.
// One cert per domain. Tracks ACME lifecycle (issue, renew, revoke) and
// stores filesystem paths to the certificate and key.
type SSLCertificate struct {
	ID            string     `gorm:"type:char(26);primaryKey"                    json:"id"`
	DomainID      string     `gorm:"type:char(26);not null;uniqueIndex:uniq_ssl_cert_domain;index:ix_ssl_cert_domain" json:"domain_id"`
	Status        string     `gorm:"type:varchar(32);not null;default:'pending'"  json:"status"`
	IssuedAt      *time.Time `gorm:"type:datetime(6)"                            json:"issued_at,omitempty"`
	ExpiresAt     *time.Time `gorm:"type:datetime(6);index:ix_ssl_cert_expires"   json:"expires_at,omitempty"`
	RenewalCount  int        `gorm:"type:int;not null;default:0"                 json:"renewal_count"`
	LastRenewedAt *time.Time `gorm:"type:datetime(6)"                            json:"last_renewed_at,omitempty"`
	LastError     *string    `gorm:"type:text"                                   json:"last_error,omitempty"`
	Staging       bool       `gorm:"type:tinyint(1);not null;default:0"           json:"staging"`
	CertPath      *string    `gorm:"type:varchar(512)"                           json:"cert_path,omitempty"`
	KeyPath       *string    `gorm:"type:varchar(512)"                           json:"key_path,omitempty"`
	CreatedAt     time.Time  `gorm:"type:datetime(6);not null"                   json:"created_at"`
	UpdatedAt     time.Time  `gorm:"type:datetime(6);not null"                   json:"updated_at"`
}

func (SSLCertificate) TableName() string { return "ssl_certificates" }
