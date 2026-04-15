package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// UserRepository is the interface handlers + services depend on. A *gorm.DB
// backed implementation is returned by NewUserRepository; tests can provide
// their own mock that satisfies this surface.
type UserRepository interface {
	Create(ctx context.Context, u *models.User) error
	FindByID(ctx context.Context, id string) (*models.User, error)
	FindByEmail(ctx context.Context, email string) (*models.User, error)
	List(ctx context.Context, offset, limit int) ([]models.User, int64, error)
	Update(ctx context.Context, u *models.User) error
	Delete(ctx context.Context, id string) error
}

type userRepo struct{ db *gorm.DB }

// NewUserRepository returns a UserRepository backed by the given GORM handle.
func NewUserRepository(db *gorm.DB) UserRepository { return &userRepo{db: db} }

func (r *userRepo) Create(ctx context.Context, u *models.User) error {
	if err := r.db.WithContext(ctx).Create(u).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *userRepo) FindByID(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	if err := r.db.WithContext(ctx).First(&u, "id = ?", id).Error; err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (r *userRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	if err := r.db.WithContext(ctx).First(&u, "email = ?", email).Error; err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (r *userRepo) List(ctx context.Context, offset, limit int) ([]models.User, int64, error) {
	var (
		out   []models.User
		total int64
	)
	q := r.db.WithContext(ctx).Model(&models.User{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, translate(err)
	}
	if err := q.Offset(offset).Limit(limit).Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, 0, translate(err)
	}
	return out, total, nil
}

func (r *userRepo) Update(ctx context.Context, u *models.User) error {
	// Select columns explicitly to keep handlers from accidentally flipping
	// is_admin via Save(). The admin-only endpoint bypasses this repo method
	// and updates is_admin directly.
	if err := r.db.WithContext(ctx).Model(u).Select(
		"email", "name_first", "name_last", "password_hash", "linux_uid",
	).Updates(u).Error; err != nil {
		return translate(err)
	}
	return nil
}

func (r *userRepo) Delete(ctx context.Context, id string) error {
	// Soft delete (gorm.DeletedAt on the model).
	res := r.db.WithContext(ctx).Delete(&models.User{}, "id = ?", id)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// translate maps infrastructure errors to repository sentinels. Handlers only
// need to distinguish not-found and conflict; everything else is bubbled up.
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) && my.Number == 1062 {
		// 1062 = ER_DUP_ENTRY (unique-key violation)
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
