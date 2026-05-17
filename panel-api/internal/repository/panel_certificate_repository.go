package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// PanelCertificateRepository owns the panel_certificate rows. Post
// ADR-0105 there are two rows discriminated by `kind` ∈
// {hostname, mail}; each is an independent cert lifecycle. The
// non-kind methods operate on the hostname kind so existing callers
// keep their behaviour while the mail path is added additively.
type PanelCertificateRepository interface {
	Get(ctx context.Context) (*models.PanelCertificate, error)
	GetByKind(ctx context.Context, kind string) (*models.PanelCertificate, error)
	ListAll(ctx context.Context) ([]*models.PanelCertificate, error)
	EnsureDefault(ctx context.Context, hostname string) (*models.PanelCertificate, error)
	Upsert(ctx context.Context, c *models.PanelCertificate) error
	MarkIssued(ctx context.Context, issuedAt, expiresAt time.Time) error
	MarkPendingRetry(ctx context.Context, errMsg string, retryAfter time.Duration) error
	MarkIssuedKind(ctx context.Context, kind string, issuedAt, expiresAt time.Time) error
	MarkPendingRetryKind(ctx context.Context, kind, errMsg string, retryAfter time.Duration) error
}

type panelCertRepo struct{ db *gorm.DB }

// NewPanelCertificateRepository returns a repository backed by db.
func NewPanelCertificateRepository(db *gorm.DB) PanelCertificateRepository {
	return &panelCertRepo{db: db}
}

func kindOrHostname(k string) string {
	if k == "" {
		return models.PanelCertKindHostname
	}
	return k
}

// panelCertPathForKind is the default install target per kind. The
// hostname cert keeps /etc/jabali/tls/panel.crt; the mail cert lands
// beside it as panel-mail.crt (ADR-0105).
func panelCertPathForKind(kind string) string {
	if kind == models.PanelCertKindMail {
		return "/etc/jabali/tls/panel-mail.crt"
	}
	return "/etc/jabali/tls/panel.crt"
}

func (r *panelCertRepo) GetByKind(ctx context.Context, kind string) (*models.PanelCertificate, error) {
	var c models.PanelCertificate
	if err := r.db.WithContext(ctx).Where("kind = ?", kindOrHostname(kind)).First(&c).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *panelCertRepo) Get(ctx context.Context) (*models.PanelCertificate, error) {
	return r.GetByKind(ctx, models.PanelCertKindHostname)
}

func (r *panelCertRepo) ListAll(ctx context.Context) ([]*models.PanelCertificate, error) {
	var rows []*models.PanelCertificate
	if err := r.db.WithContext(ctx).Order("kind").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *panelCertRepo) ensureOne(ctx context.Context, kind, hostname string) (*models.PanelCertificate, error) {
	existing, err := r.GetByKind(ctx, kind)
	if err == nil {
		if existing.Hostname != hostname && hostname != "" {
			existing.Hostname = hostname
			if uerr := r.Upsert(ctx, existing); uerr != nil {
				return nil, uerr
			}
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	row := &models.PanelCertificate{
		Kind:        kind,
		ID:          1,
		Hostname:    hostname,
		Status:      models.PanelCertStatusSelfSigned,
		CertPEMPath: panelCertPathForKind(kind),
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

func (r *panelCertRepo) EnsureDefault(ctx context.Context, hostname string) (*models.PanelCertificate, error) {
	host, err := r.ensureOne(ctx, models.PanelCertKindHostname, hostname)
	if err != nil {
		return nil, err
	}
	if _, err := r.ensureOne(ctx, models.PanelCertKindMail, models.PanelMailHostname(hostname)); err != nil {
		return nil, err
	}
	return host, nil
}

// Upsert mirrors ServerSettingsRepository.Upsert: explicit
// exists-check + Create/Updates instead of Save() to avoid GORM's
// ON DUPLICATE path tripping over the string primary key.
func (r *panelCertRepo) Upsert(ctx context.Context, c *models.PanelCertificate) error {
	c.Kind = kindOrHostname(c.Kind)
	if c.ID == 0 {
		c.ID = 1
	}

	var existing models.PanelCertificate
	err := r.db.WithContext(ctx).Where("kind = ?", c.Kind).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&existing).Where("kind = ?", c.Kind).Select("*").Omit("kind").Updates(c).Error
}

func (r *panelCertRepo) markIssued(ctx context.Context, kind string, issuedAt, expiresAt time.Time) error {
	return r.db.WithContext(ctx).
		Model(&models.PanelCertificate{}).
		Where("kind = ?", kindOrHostname(kind)).
		Updates(map[string]any{
			"status":        models.PanelCertStatusIssued,
			"issued_at":     issuedAt.UTC(),
			"expires_at":    expiresAt.UTC(),
			"last_error":    "",
			"attempt_count": 0,
			"next_retry_at": nil,
		}).Error
}

func (r *panelCertRepo) markPendingRetry(ctx context.Context, kind, errMsg string, retryAfter time.Duration) error {
	next := time.Now().Add(retryAfter).UTC()
	return r.db.WithContext(ctx).
		Model(&models.PanelCertificate{}).
		Where("kind = ?", kindOrHostname(kind)).
		Updates(map[string]any{
			"status":        models.PanelCertStatusPendingACMERetry,
			"last_error":    errMsg,
			"attempt_count": gorm.Expr("attempt_count + 1"),
			"next_retry_at": next,
		}).Error
}

func (r *panelCertRepo) MarkIssued(ctx context.Context, issuedAt, expiresAt time.Time) error {
	return r.markIssued(ctx, models.PanelCertKindHostname, issuedAt, expiresAt)
}

func (r *panelCertRepo) MarkPendingRetry(ctx context.Context, errMsg string, retryAfter time.Duration) error {
	return r.markPendingRetry(ctx, models.PanelCertKindHostname, errMsg, retryAfter)
}

func (r *panelCertRepo) MarkIssuedKind(ctx context.Context, kind string, issuedAt, expiresAt time.Time) error {
	return r.markIssued(ctx, kind, issuedAt, expiresAt)
}

func (r *panelCertRepo) MarkPendingRetryKind(ctx context.Context, kind, errMsg string, retryAfter time.Duration) error {
	return r.markPendingRetry(ctx, kind, errMsg, retryAfter)
}
