package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PanelCertificateRepository owns the singleton panel_certificate row.
// Implementations must enforce id=1.
type PanelCertificateRepository interface {
	// Get returns the singleton row. ErrNotFound when absent — callers
	// usually pair this with EnsureDefault on first read.
	Get(ctx context.Context) (*models.PanelCertificate, error)

	// EnsureDefault inserts the singleton row with status=self_signed
	// and use_le=0 if it doesn't already exist. Idempotent: existing
	// rows are returned untouched. Pattern mirrors the ManagedIP
	// EnsureDefault path called from serve.go's first-boot seed.
	EnsureDefault(ctx context.Context, hostname string) (*models.PanelCertificate, error)

	// Upsert writes the row, forcing id=1.
	Upsert(ctx context.Context, c *models.PanelCertificate) error

	// MarkIssued is a small focused helper used by the reconciler /
	// agent-result handler so common state transitions don't go
	// through a full-row Upsert (which would risk overwriting a field
	// that another goroutine raced in). Sets status=issued, clears
	// last_error, sets issued_at + expires_at + attempt_count=0,
	// next_retry_at=NULL.
	MarkIssued(ctx context.Context, issuedAt, expiresAt time.Time) error

	// MarkPendingRetry is the failure-path counterpart. Sets status to
	// pending_acme_retry, increments attempt_count, stores last_error,
	// sets next_retry_at = now + retryAfter (caller picks; see plan
	// for the 3h default).
	MarkPendingRetry(ctx context.Context, errMsg string, retryAfter time.Duration) error
}

type panelCertRepo struct{ db *gorm.DB }

// NewPanelCertificateRepository returns a repository backed by db.
func NewPanelCertificateRepository(db *gorm.DB) PanelCertificateRepository {
	return &panelCertRepo{db: db}
}

func (r *panelCertRepo) Get(ctx context.Context) (*models.PanelCertificate, error) {
	var c models.PanelCertificate
	if err := r.db.WithContext(ctx).First(&c, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *panelCertRepo) EnsureDefault(ctx context.Context, hostname string) (*models.PanelCertificate, error) {
	if existing, err := r.Get(ctx); err == nil {
		// Hostname drift: keep the row but update hostname so the
		// reconciler routability check sees the current value.
		if existing.Hostname != hostname && hostname != "" {
			existing.Hostname = hostname
			if err := r.Upsert(ctx, existing); err != nil {
				return nil, err
			}
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	row := &models.PanelCertificate{
		ID:          1,
		Hostname:    hostname,
		Status:      models.PanelCertStatusSelfSigned,
		CertPEMPath: "/etc/jabali/tls/panel.crt",
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// Upsert mirrors ServerSettingsRepository.Upsert: explicit
// exists-check + Create/Updates instead of Save() because GORM's
// `uint8 primaryKey default:1` tag triggers MariaDB error 1110
// ("Column 'id' specified twice") on Save's INSERT ... ON DUPLICATE.
func (r *panelCertRepo) Upsert(ctx context.Context, c *models.PanelCertificate) error {
	c.ID = 1

	var existing models.PanelCertificate
	err := r.db.WithContext(ctx).First(&existing, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).Select("*").Omit("id").Updates(c).Error
}

func (r *panelCertRepo) MarkIssued(ctx context.Context, issuedAt, expiresAt time.Time) error {
	return r.db.WithContext(ctx).
		Model(&models.PanelCertificate{}).
		Where("id = ?", 1).
		Updates(map[string]any{
			"status":         models.PanelCertStatusIssued,
			"issued_at":      issuedAt.UTC(),
			"expires_at":     expiresAt.UTC(),
			"last_error":     "",
			"attempt_count":  0,
			"next_retry_at":  nil,
		}).Error
}

func (r *panelCertRepo) MarkPendingRetry(ctx context.Context, errMsg string, retryAfter time.Duration) error {
	next := time.Now().Add(retryAfter).UTC()
	return r.db.WithContext(ctx).
		Model(&models.PanelCertificate{}).
		Where("id = ?", 1).
		Updates(map[string]any{
			"status":         models.PanelCertStatusPendingACMERetry,
			"last_error":     errMsg,
			"attempt_count":  gorm.Expr("attempt_count + 1"),
			"next_retry_at":  next,
		}).Error
}
