package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// SSLCertificateWithDomain is a read-only projection joining ssl_certificates, domains, and users.
// Used by list endpoints to provide a unified view without follow-up queries.
type SSLCertificateWithDomain struct {
	ID            string     `json:"id"`
	DomainID      string     `json:"domain_id"`
	DomainName    string     `json:"domain_name"`
	UserID        string     `json:"user_id"`
	UserUsername  string     `json:"user_username"`
	Status        string     `json:"status"`
	IssuedAt      *time.Time `json:"issued_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	RenewalCount  int        `json:"renewal_count"`
	LastRenewedAt *time.Time `json:"last_renewed_at"`
	LastError     *string    `json:"last_error"`
	Staging       bool       `json:"staging"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
}

// SSLCertificateRepository covers the ssl_certificates table.
// Tracks ACME certificate lifecycle per domain.
type SSLCertificateRepository interface {
	Create(ctx context.Context, cert *models.SSLCertificate) error
	FindByDomainID(ctx context.Context, domainID string) (*models.SSLCertificate, error)
	FindByDomainIDs(ctx context.Context, domainIDs []string) ([]models.SSLCertificate, error)
	UpdateStatus(ctx context.Context, id string, status string, lastError *string) error
	UpdateAfterIssuance(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error
	UpdateAfterRenewal(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error
	MarkRevoked(ctx context.Context, id string) error
	DeleteByDomainID(ctx context.Context, domainID string) error
	ListDueForRenewal(ctx context.Context, within time.Duration) ([]models.SSLCertificate, error)
	ListAll(ctx context.Context) ([]SSLCertificateWithDomain, error)
	ListByUserID(ctx context.Context, userID string) ([]SSLCertificateWithDomain, error)
	UpdateSelfSigned(ctx context.Context, id string, certPath, keyPath string, expiresAt time.Time) error
	UpdateAfterACMEFailure(ctx context.Context, id string, lastError string, nextRetryAt time.Time, retryCount int, fallbackCertPath, fallbackKeyPath *string, fallbackExpiresAt *time.Time) error
	MarkFailed(ctx context.Context, id string, lastError string) error
	ListDueForACMERetry(ctx context.Context, now time.Time, limit int) ([]models.SSLCertificate, error)
}

type sslCertificateRepo struct{ db *gorm.DB }

// NewSSLCertificateRepository returns an SSLCertificateRepository bound to a GORM DB.
func NewSSLCertificateRepository(db *gorm.DB) SSLCertificateRepository {
	return &sslCertificateRepo{db: db}
}

// Create inserts a new SSL certificate record.
func (r *sslCertificateRepo) Create(ctx context.Context, cert *models.SSLCertificate) error {
	return r.db.WithContext(ctx).Create(cert).Error
}

// FindByDomainID retrieves the SSL certificate for a domain.
// Returns ErrNotFound if no certificate exists; other errors are DB errors.
func (r *sslCertificateRepo) FindByDomainID(ctx context.Context, domainID string) (*models.SSLCertificate, error) {
	var cert models.SSLCertificate
	err := r.db.WithContext(ctx).First(&cert, "domain_id = ?", domainID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// UpdateStatus updates the certificate's status and optional last error.
// Useful for transitions like pending → issuing or issuing → issued/failed.
func (r *sslCertificateRepo) UpdateStatus(ctx context.Context, id string, status string, lastError *string) error {
	updates := map[string]interface{}{
		"status":     status,
		"last_error": lastError,
		"updated_at": time.Now(),
	}
	if status == models.SSLStatusFailed {
		updates["last_attempt_at"] = time.Now()
	}
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(updates).Error
}

// UpdateAfterIssuance updates issuance metadata: issued_at, expires_at, cert_path, key_path.
// Called after a successful ACME cert issue or renewal.
func (r *sslCertificateRepo) UpdateAfterIssuance(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error {
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"issued_at":       issuedAt,
			"expires_at":      expiresAt,
			"cert_path":       certPath,
			"key_path":        keyPath,
			"status":          models.SSLStatusIssued,
			"last_attempt_at": time.Now(),
			"updated_at":      time.Now(),
		}).Error
}

// UpdateAfterRenewal does what UpdateAfterIssuance does plus bumps the
// renewal_count and stamps last_renewed_at. Used by the reconciler after a
// successful ssl.renew agent call.
func (r *sslCertificateRepo) UpdateAfterRenewal(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"issued_at":       issuedAt,
			"expires_at":      expiresAt,
			"cert_path":       certPath,
			"key_path":        keyPath,
			"status":          models.SSLStatusIssued,
			"last_renewed_at": now,
			"renewal_count":   gorm.Expr("renewal_count + 1"),
			"last_error":      nil,
			"last_attempt_at": now,
			"updated_at":      now,
		}).Error
}

// MarkRevoked flips status='revoked' and clears cert/key paths + last_error.
// Called after a successful ssl.revoke; the nginx vhost regen that runs next
// will read the cleared paths and drop the 443 server block.
func (r *sslCertificateRepo) MarkRevoked(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     models.SSLStatusRevoked,
			"cert_path":  nil,
			"key_path":   nil,
			"last_error": nil,
			"updated_at": time.Now(),
		}).Error
}

// DeleteByDomainID removes the SSL certificate for a domain.
// Called when SSL is disabled for a domain or domain is deleted.
func (r *sslCertificateRepo) DeleteByDomainID(ctx context.Context, domainID string) error {
	return r.db.WithContext(ctx).Delete(&models.SSLCertificate{}, "domain_id = ?", domainID).Error
}

// ListDueForRenewal returns all certificates in 'issued' status
// whose expiration is within the given duration from now.
// The renewal ticker uses this to find candidates for renewal.
// E.g., within=30*24*time.Hour finds certs expiring within 30 days.
func (r *sslCertificateRepo) ListDueForRenewal(ctx context.Context, within time.Duration) ([]models.SSLCertificate, error) {
	var certs []models.SSLCertificate
	now := time.Now()
	deadline := now.Add(within)
	err := r.db.WithContext(ctx).
		Where("status = ? AND expires_at IS NOT NULL AND expires_at < ?",
			models.SSLStatusIssued, deadline).
		Find(&certs).Error
	if err != nil {
		return nil, err
	}
	return certs, nil
}

// ListAll returns all SSL certificates joined with their domain and user info.
// Used by admin to view all certificates across all domains and users.
func (r *sslCertificateRepo) ListAll(ctx context.Context) ([]SSLCertificateWithDomain, error) {
	var results []SSLCertificateWithDomain
	err := r.db.WithContext(ctx).
		Select(`sc.id, sc.domain_id, d.name as domain_name,
		        d.user_id, u.username as user_username,
		        sc.status, sc.issued_at, sc.expires_at,
		        sc.renewal_count, sc.last_renewed_at, sc.last_error, sc.staging, sc.last_attempt_at`).
		Table("ssl_certificates sc").
		Joins("JOIN domains d ON sc.domain_id = d.id").
		Joins("JOIN users u ON d.user_id = u.id").
		Order("sc.created_at DESC").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}
	return results, nil
}

// ListByUserID returns all SSL certificates for a specific user,
// joining with domain and user info. Used by users to view their own certificates.
func (r *sslCertificateRepo) ListByUserID(ctx context.Context, userID string) ([]SSLCertificateWithDomain, error) {
	var results []SSLCertificateWithDomain
	err := r.db.WithContext(ctx).
		Select(`sc.id, sc.domain_id, d.name as domain_name,
		        d.user_id, u.username as user_username,
		        sc.status, sc.issued_at, sc.expires_at,
		        sc.renewal_count, sc.last_renewed_at, sc.last_error, sc.staging, sc.last_attempt_at`).
		Table("ssl_certificates sc").
		Joins("JOIN domains d ON sc.domain_id = d.id").
		Joins("JOIN users u ON d.user_id = u.id").
		Where("d.user_id = ?", userID).
		Order("sc.created_at DESC").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}
	return results, nil
}

// UpdateSelfSigned sets the certificate to self-signed fallback status
// with the given cert/key paths and expiration, clearing last_error.
func (r *sslCertificateRepo) UpdateSelfSigned(ctx context.Context, id string, certPath, keyPath string, expiresAt time.Time) error {
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":           models.SSLStatusSelfSigned,
			"cert_path":        certPath,
			"key_path":         keyPath,
			"expires_at":       expiresAt,
			"last_error":       nil,
			"last_attempt_at":  time.Now(),
			"updated_at":       time.Now(),
		}).Error
}

// UpdateAfterACMEFailure sets status to pending_acme_retry, records the error,
// and schedules the next retry via next_retry_at and retry_count.
// If fallback paths are provided, also writes those (for first failure with self-signed).
func (r *sslCertificateRepo) UpdateAfterACMEFailure(ctx context.Context, id string, lastError string, nextRetryAt time.Time, retryCount int, fallbackCertPath, fallbackKeyPath *string, fallbackExpiresAt *time.Time) error {
	updates := map[string]interface{}{
		"status":          models.SSLStatusPendingACMERetry,
		"last_error":      lastError,
		"next_retry_at":   nextRetryAt,
		"retry_count":     retryCount,
		"last_attempt_at": time.Now(),
		"updated_at":      time.Now(),
	}
	if fallbackCertPath != nil {
		updates["cert_path"] = *fallbackCertPath
	}
	if fallbackKeyPath != nil {
		updates["key_path"] = *fallbackKeyPath
	}
	if fallbackExpiresAt != nil {
		updates["expires_at"] = *fallbackExpiresAt
	}
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(updates).Error
}

// MarkFailed sets status='failed' and clears next_retry_at (manual retry only).
func (r *sslCertificateRepo) MarkFailed(ctx context.Context, id string, lastError string) error {
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          models.SSLStatusFailed,
			"last_error":      lastError,
			"next_retry_at":   nil,
			"last_attempt_at": time.Now(),
			"updated_at":      time.Now(),
		}).Error
}

// ListDueForACMERetry returns certificates the SSL retry ticker should attempt
// to (re-)issue right now. Two cases qualify:
//
//  1. status='pending' — the row was just created (or operator-reset) and has
//     never had ACME run against it. There is no next_retry_at yet, so the
//     ticker is the first thing to pick it up after the API hands it off.
//
//  2. status='pending_acme_retry' AND next_retry_at <= now — the row has had
//     at least one failed ACME attempt and is now due for the next try.
//
// 'issued' / 'self_signed' / 'failed' / 'renewing' are deliberately excluded:
// 'issued' is steady state (renewals go through the renewal ticker), 'failed'
// is operator-only (manual reset to 'pending'), and 'renewing' is in-flight.
func (r *sslCertificateRepo) ListDueForACMERetry(ctx context.Context, now time.Time, limit int) ([]models.SSLCertificate, error) {
	var certs []models.SSLCertificate
	err := r.db.WithContext(ctx).
		Where("status = ? OR (status = ? AND next_retry_at IS NOT NULL AND next_retry_at <= ?)",
			models.SSLStatusPending,
			models.SSLStatusPendingACMERetry, now).
		Order("created_at ASC").
		Limit(limit).
		Find(&certs).Error
	if err != nil {
		return nil, err
	}
	return certs, nil
}

// FindByDomainIDs fetches SSL certificates for multiple domains
func (r *sslCertificateRepo) FindByDomainIDs(ctx context.Context, domainIDs []string) ([]models.SSLCertificate, error) {
	var certs []models.SSLCertificate
	err := r.db.WithContext(ctx).Where("domain_id IN ?", domainIDs).Find(&certs).Error
	if err != nil {
		return nil, err
	}
	return certs, nil
}
