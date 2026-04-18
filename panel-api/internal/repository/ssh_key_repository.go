package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// SSHKeyRepository defines data access for SSH public keys.
type SSHKeyRepository interface {
	Create(ctx context.Context, key *models.SSHKey) error
	FindByID(ctx context.Context, id string) (*models.SSHKey, error)
	FindByIDAndUserID(ctx context.Context, id, userID string) (*models.SSHKey, error)
	ListByUserID(ctx context.Context, userID string) ([]models.SSHKey, error)
	List(ctx context.Context) ([]models.SSHKey, error)
	Delete(ctx context.Context, id string) error
	DeleteByUserID(ctx context.Context, userID string) error
	CountByUserID(ctx context.Context, userID string) (int64, error)
}

type sshKeyRepo struct{ db *gorm.DB }

func NewSSHKeyRepository(db *gorm.DB) SSHKeyRepository {
	return &sshKeyRepo{db: db}
}

func (r *sshKeyRepo) Create(ctx context.Context, key *models.SSHKey) error {
	if err := r.db.WithContext(ctx).Create(key).Error; err != nil {
		return translateSSHKey(err)
	}
	return nil
}

func (r *sshKeyRepo) FindByID(ctx context.Context, id string) (*models.SSHKey, error) {
	var key models.SSHKey
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &key, nil
}

func (r *sshKeyRepo) FindByIDAndUserID(ctx context.Context, id, userID string) (*models.SSHKey, error) {
	var key models.SSHKey
	if err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &key, nil
}

func (r *sshKeyRepo) ListByUserID(ctx context.Context, userID string) ([]models.SSHKey, error) {
	var keys []models.SSHKey
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

func (r *sshKeyRepo) List(ctx context.Context) ([]models.SSHKey, error) {
	var keys []models.SSHKey
	if err := r.db.WithContext(ctx).Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

func (r *sshKeyRepo) Delete(ctx context.Context, id string) error {
	if err := r.db.WithContext(ctx).Where("id = ?", id).Delete(&models.SSHKey{}).Error; err != nil {
		return err
	}
	return nil
}

func (r *sshKeyRepo) DeleteByUserID(ctx context.Context, userID string) error {
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&models.SSHKey{}).Error; err != nil {
		return err
	}
	return nil
}

func (r *sshKeyRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.SSHKey{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// translateSSHKey translates GORM and database errors to repository error conventions.
func translateSSHKey(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) && my.Number == 1062 {
		// 1062 = ER_DUP_ENTRY (unique-key violation on user_id, fingerprint)
		return ErrConflict
	}
	// Some MariaDB deployments surface constraint names in the message; a
	// cheap substring check catches the remaining duplicate-key cases even
	// if the driver doesn't give us a typed error.
	if strings.Contains(err.Error(), "Duplicate entry") {
		return ErrConflict
	}
	return err
}
