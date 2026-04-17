package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// DatabaseUserGrantRepository defines data access for database user grants.
type DatabaseUserGrantRepository interface {
	FindByID(ctx context.Context, id string) (*models.DatabaseUserGrant, error)
	ListByDatabaseID(ctx context.Context, databaseID string) ([]models.DatabaseUserGrant, error)
	ListByDatabaseUserID(ctx context.Context, databaseUserID string) ([]models.DatabaseUserGrant, error)
}

type databaseUserGrantRepo struct{ db *gorm.DB }

func NewDatabaseUserGrantRepository(db *gorm.DB) DatabaseUserGrantRepository {
	return &databaseUserGrantRepo{db: db}
}

func (r *databaseUserGrantRepo) FindByID(ctx context.Context, id string) (*models.DatabaseUserGrant, error) {
	var g models.DatabaseUserGrant
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&g).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &g, nil
}

func (r *databaseUserGrantRepo) ListByDatabaseID(ctx context.Context, databaseID string) ([]models.DatabaseUserGrant, error) {
	var grants []models.DatabaseUserGrant
	if err := r.db.WithContext(ctx).Where("database_id = ?", databaseID).Find(&grants).Error; err != nil {
		return nil, err
	}
	return grants, nil
}

func (r *databaseUserGrantRepo) ListByDatabaseUserID(ctx context.Context, databaseUserID string) ([]models.DatabaseUserGrant, error) {
	var grants []models.DatabaseUserGrant
	if err := r.db.WithContext(ctx).Where("database_user_id = ?", databaseUserID).Find(&grants).Error; err != nil {
		return nil, err
	}
	return grants, nil
}
