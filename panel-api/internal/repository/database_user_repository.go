package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// DatabaseUserRepository defines data access for database users.
type DatabaseUserRepository interface {
	FindByID(ctx context.Context, id string) (*models.DatabaseUser, error)
	List(ctx context.Context, opts ListOptions) ([]models.DatabaseUser, int64, error)
	ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.DatabaseUser, int64, error)
	CountByUserID(ctx context.Context, userID string) (int64, error)
}

type databaseUserRepo struct{ db *gorm.DB }

func NewDatabaseUserRepository(db *gorm.DB) DatabaseUserRepository {
	return &databaseUserRepo{db: db}
}

func (r *databaseUserRepo) FindByID(ctx context.Context, id string) (*models.DatabaseUser, error) {
	var du models.DatabaseUser
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&du).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &du, nil
}

// databaseUserListCols — only the database username is free-text searchable.
var databaseUserListCols = ListCols{
	Search:      []string{"username"},
	Sort:        []string{"username", "created_at"},
	DefaultSort: "created_at DESC",
}

func (r *databaseUserRepo) List(ctx context.Context, opts ListOptions) ([]models.DatabaseUser, int64, error) {
	var (
		users []models.DatabaseUser
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.DatabaseUser{})

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, databaseUserListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, databaseUserListCols)
	if err := q.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (r *databaseUserRepo) ListByUserID(ctx context.Context, userID string, opts ListOptions) ([]models.DatabaseUser, int64, error) {
	var (
		users []models.DatabaseUser
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.DatabaseUser{}).Where("user_id = ?", userID)

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, databaseUserListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, databaseUserListCols)
	if err := q.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (r *databaseUserRepo) CountByUserID(ctx context.Context, userID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&models.DatabaseUser{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
