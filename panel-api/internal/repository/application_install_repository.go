package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// ApplicationInstallRepository defines data access for installed apps.
// One row per (domain, subdirectory, app_type) — see migration 000046.
// The interface is named for the M19 generalisation; the legacy alias
// `WordPressInstallRepository` (below) keeps WP-specific call sites
// compiling through the M19 release window.
type ApplicationInstallRepository interface {
	Create(ctx context.Context, install *models.ApplicationInstall) error
	FindByID(ctx context.Context, id string) (*models.ApplicationInstall, error)
	FindByIDAndUserID(ctx context.Context, id, userID string) (*models.ApplicationInstall, error)
	FindByDomainID(ctx context.Context, domainID string) (*models.ApplicationInstall, error)
	// FindByDomainAndSubdirectory enforces install uniqueness at the
	// (domain, subdirectory) granularity that matches the on-disk install
	// path. Empty subdirectory = docroot install. PRE-M19 callers used
	// this for the duplicate-install precheck; post-M19 the precheck
	// SHOULD use FindByDomainAndSubdirectoryAndAppType so two distinct
	// app types (e.g. WordPress + DokuWiki) can share a (domain, subdir).
	FindByDomainAndSubdirectory(ctx context.Context, domainID, subdirectory string) (*models.ApplicationInstall, error)
	// FindByDomainAndSubdirectoryAndAppType returns the install at the
	// exact (domain, subdir, app_type) coordinate. Use this for the
	// 409 install_exists check on POST /applications — different app
	// types in the same (domain, subdir) slot are allowed by design.
	FindByDomainAndSubdirectoryAndAppType(ctx context.Context, domainID, subdirectory, appType string) (*models.ApplicationInstall, error)
	FindByDBID(ctx context.Context, dbID string) (*models.ApplicationInstall, error)
	ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.ApplicationInstall, int64, error)
	List(ctx context.Context, opts ListOptions) ([]models.ApplicationInstall, int64, error)
	UpdateStatus(ctx context.Context, id, status string, lastError *string, version *string) error
	// UpdateOIDCFields persists the Hydra-minted OAuth 2 client id + the
	// AES-256-GCM-sealed client_secret on an existing install row. Called
	// by the M16 Wave D client-provisioning path in applications_service.go
	// AFTER hydraclient.CreateClient returns — the row exists first, then
	// the OIDC columns are back-filled. secretEnc is the envelope
	// produced by ssokey.Key.Seal (nonce(12) || ciphertext || auth_tag(16));
	// callers MUST NOT persist plaintext.
	UpdateOIDCFields(ctx context.Context, id, oidcClientID string, secretEnc []byte) error
	Delete(ctx context.Context, id string) error
}

// WordPressInstallRepository is the pre-M19 alias. Same interface, kept
// so wordpress.go handler code compiles unchanged through M19. M19.1
// deletes this alias.
type WordPressInstallRepository = ApplicationInstallRepository

type applicationInstallRepo struct{ db *gorm.DB }

// NewApplicationInstallRepository constructs the GORM-backed repo.
func NewApplicationInstallRepository(db *gorm.DB) ApplicationInstallRepository {
	return &applicationInstallRepo{db: db}
}

// NewWordPressInstallRepository is the pre-M19 constructor name. Kept as
// a thin alias so app.go's wiring code compiles unchanged through M19.
func NewWordPressInstallRepository(db *gorm.DB) WordPressInstallRepository {
	return NewApplicationInstallRepository(db)
}

func (r *applicationInstallRepo) Create(ctx context.Context, install *models.ApplicationInstall) error {
	if err := r.db.WithContext(ctx).Create(install).Error; err != nil {
		return err
	}
	return nil
}

func (r *applicationInstallRepo) FindByID(ctx context.Context, id string) (*models.ApplicationInstall, error) {
	var install models.ApplicationInstall
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *applicationInstallRepo) FindByIDAndUserID(ctx context.Context, id, userID string) (*models.ApplicationInstall, error) {
	var install models.ApplicationInstall
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *applicationInstallRepo) FindByDomainID(ctx context.Context, domainID string) (*models.ApplicationInstall, error) {
	var install models.ApplicationInstall
	if err := r.db.WithContext(ctx).Where("domain_id = ?", domainID).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *applicationInstallRepo) FindByDomainAndSubdirectory(ctx context.Context, domainID, subdirectory string) (*models.ApplicationInstall, error) {
	var install models.ApplicationInstall
	if err := r.db.WithContext(ctx).
		Where("domain_id = ? AND subdirectory = ?", domainID, subdirectory).
		First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *applicationInstallRepo) FindByDomainAndSubdirectoryAndAppType(ctx context.Context, domainID, subdirectory, appType string) (*models.ApplicationInstall, error) {
	var install models.ApplicationInstall
	if err := r.db.WithContext(ctx).
		Where("domain_id = ? AND subdirectory = ? AND app_type = ?", domainID, subdirectory, appType).
		First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

func (r *applicationInstallRepo) FindByDBID(ctx context.Context, dbID string) (*models.ApplicationInstall, error) {
	var install models.ApplicationInstall
	if err := r.db.WithContext(ctx).Where("db_id = ?", dbID).First(&install).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &install, nil
}

var applicationInstallListCols = ListCols{
	Search:      []string{"admin_email"},
	Sort:        []string{"admin_email", "status", "created_at"},
	DefaultSort: "created_at",
}

func (r *applicationInstallRepo) ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.ApplicationInstall, int64, error) {
	var (
		installs []models.ApplicationInstall
		total    int64
	)
	base := r.db.WithContext(ctx).Model(&models.ApplicationInstall{}).Where("user_id = ?", userID)

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, applicationInstallListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, applicationInstallListCols)
	if err := q.Find(&installs).Error; err != nil {
		return nil, 0, err
	}
	return installs, total, nil
}

func (r *applicationInstallRepo) List(ctx context.Context, opts ListOptions) ([]models.ApplicationInstall, int64, error) {
	var (
		installs []models.ApplicationInstall
		total    int64
	)
	base := r.db.WithContext(ctx).Model(&models.ApplicationInstall{})

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, applicationInstallListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, applicationInstallListCols)
	if err := q.Find(&installs).Error; err != nil {
		return nil, 0, err
	}
	return installs, total, nil
}

func (r *applicationInstallRepo) UpdateStatus(ctx context.Context, id, status string, lastError *string, version *string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if lastError != nil {
		updates["last_error"] = *lastError
	}
	if version != nil {
		updates["version"] = *version
	}
	if err := r.db.WithContext(ctx).Model(&models.ApplicationInstall{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return err
	}
	return nil
}

func (r *applicationInstallRepo) UpdateOIDCFields(ctx context.Context, id, oidcClientID string, secretEnc []byte) error {
	// Model(&ApplicationInstall{}).Where(id).Updates(map) + pointer form
	// for oidc_client_id so GORM writes a real CHAR(40) value (not the
	// zero-value empty string the Updates(struct) shape would skip).
	updates := map[string]interface{}{
		"oidc_client_id":          oidcClientID,
		"oidc_client_secret_enc":  secretEnc,
	}
	res := r.db.WithContext(ctx).Model(&models.ApplicationInstall{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *applicationInstallRepo) Delete(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Delete(&models.ApplicationInstall{}, "id = ?", id)
	if err := result.Error; err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
