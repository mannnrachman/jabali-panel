package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// SSLCertificateRepository covers the ssl_certificates table.
// Tracks ACME certificate lifecycle per domain.
type SSLCertificateRepository interface {
	Create(ctx context.Context, cert *models.SSLCertificate) error
	FindByDomainID(ctx context.Context, domainID string) (*models.SSLCertificate, error)
	UpdateStatus(ctx context.Context, id string, status string, lastError *string) error
	UpdateAfterIssuance(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error
	UpdateAfterRenewal(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error
	MarkRevoked(ctx context.Context, id string) error
	DeleteByDomainID(ctx context.Context, domainID string) error
	ListDueForRenewal(ctx context.Context, within time.Duration) ([]models.SSLCertificate, error)
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
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"last_error":  lastError,
			"updated_at":  time.Now(),
		}).Error
}

// UpdateAfterIssuance updates issuance metadata: issued_at, expires_at, cert_path, key_path.
// Called after a successful ACME cert issue or renewal.
func (r *sslCertificateRepo) UpdateAfterIssuance(ctx context.Context, id string, issuedAt, expiresAt time.Time, certPath, keyPath string) error {
	return r.db.WithContext(ctx).Model(&models.SSLCertificate{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"issued_at":   issuedAt,
			"expires_at":  expiresAt,
			"cert_path":   certPath,
			"key_path":    keyPath,
			"status":      models.SSLStatusIssued,
			"updated_at":  time.Now(),
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
