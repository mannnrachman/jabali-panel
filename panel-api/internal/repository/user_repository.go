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
	FindByUsername(ctx context.Context, username string) (*models.User, error)
	// FindByKratosIdentityID looks up a panel user by their Kratos identity
	// UUID. Used by the Kratos middleware to resolve the session's identity
	// to the panel's own user.id (ULID), which is what ownership checks key
	// off throughout the API. Returns ErrNotFound for identities not yet
	// migrated — middleware treats that as unauthenticated.
	FindByKratosIdentityID(ctx context.Context, kratosID string) (*models.User, error)
	List(ctx context.Context, opts ListOptions) ([]models.User, int64, error)
	Update(ctx context.Context, u *models.User) error
	// LinkKratosIdentity writes kratos_identity_id on the row. Deliberately
	// separate from Update — Update's column allowlist excludes this field so
	// a profile-edit handler can't accidentally overwrite it, but the M20
	// compensating-transaction flow in BootstrapAdmin + POST /users + the CLI
	// create path all need to stamp it after the Kratos side succeeds.
	LinkKratosIdentity(ctx context.Context, userID, kratosID string) error
	// SetAdmin flips is_admin on the row. Deliberately separate from Update
	// so the profile-edit path can't accidentally escalate privileges.
	SetAdmin(ctx context.Context, id string, isAdmin bool) error
	// CountAdmins returns the number of non-deleted admin rows. Used to
	// refuse demoting / deleting the last admin and causing a lockout.
	CountAdmins(ctx context.Context) (int64, error)
	// FindAdminsByEmail returns all admin users. Panel-internal admin
	// lookups (e.g., to send them a system alert).
	FindAdminsByEmail(ctx context.Context) ([]*models.User, error)
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

func (r *userRepo) FindByUsername(ctx context.Context, username string) (*models.User, error) {
	var u models.User
	if err := r.db.WithContext(ctx).First(&u, "username = ?", username).Error; err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

func (r *userRepo) FindByKratosIdentityID(ctx context.Context, kratosID string) (*models.User, error) {
	if kratosID == "" {
		return nil, repoErrNotFound()
	}
	var u models.User
	if err := r.db.WithContext(ctx).First(&u, "kratos_identity_id = ?", kratosID).Error; err != nil {
		return nil, translate(err)
	}
	return &u, nil
}

// repoErrNotFound returns ErrNotFound as a sentinel for empty-key short-circuits.
// Kept as a function (not a constant ref) because translate() drives the normal
// path; this preserves the same "err is ErrNotFound" contract without round-
// tripping through GORM for an obviously empty lookup.
func repoErrNotFound() error { return ErrNotFound }

// userListCols — columns the API may search and sort by. password_hash
// is deliberately absent; email/name/username are the obvious admin-search
// targets. Sort allows flipping between recency and alphabetical.
var userListCols = ListCols{
	Search:      []string{"email", "username", "name_first", "name_last"},
	Sort:        []string{"email", "created_at", "is_admin"},
	DefaultSort: "created_at",
}

func (r *userRepo) List(ctx context.Context, opts ListOptions) ([]models.User, int64, error) {
	var (
		out   []models.User
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.User{})
	if opts.IsAdmin != nil {
		base = base.Where("is_admin = ?", *opts.IsAdmin)
	}

	// Count honours the search filter (so paginated total stays correct for
	// a filtered set) but ignores sort/offset/limit — those apply to the
	// fetch, not the cardinality. The is_admin filter is already baked into
	// `base` above so both count + fetch see it.
	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, userListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, translate(err)
	}

	q := applyListOptions(base.Session(&gorm.Session{}), opts, userListCols)
	if err := q.Find(&out).Error; err != nil {
		return nil, 0, translate(err)
	}
	return out, total, nil
}

func (r *userRepo) Update(ctx context.Context, u *models.User) error {
	// Select columns explicitly to keep handlers from accidentally flipping
	// is_admin via Save(). The admin-only endpoint bypasses this repo method
	// and updates is_admin directly. kratos_identity_id is also excluded here
	// — the M20 compensating-transaction callers use LinkKratosIdentity().
	if err := r.db.WithContext(ctx).Model(u).Select(
		"email", "name_first", "name_last", "password_hash", "linux_uid", "package_id",
	).Updates(u).Error; err != nil {
		return translate(err)
	}
	return nil
}

// LinkKratosIdentity writes kratos_identity_id in isolation. Returns
// ErrNotFound if no row exists for userID so the caller can distinguish
// missing-row from update-failure and trigger the correct rollback.
func (r *userRepo) LinkKratosIdentity(ctx context.Context, userID, kratosID string) error {
	res := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("id = ?", userID).
		Update("kratos_identity_id", kratosID)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *userRepo) SetAdmin(ctx context.Context, id string, isAdmin bool) error {
	res := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("id = ?", id).
		Update("is_admin", isAdmin)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *userRepo) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).
		Model(&models.User{}).
		Where("is_admin = ?", true).
		Count(&n).Error; err != nil {
		return 0, translate(err)
	}
	return n, nil
}

func (r *userRepo) FindAdminsByEmail(ctx context.Context) ([]*models.User, error) {
	var admins []*models.User
	if err := r.db.WithContext(ctx).
		Where("is_admin = ?", true).
		Order("email ASC").
		Find(&admins).Error; err != nil {
		return nil, translate(err)
	}
	return admins, nil
}

func (r *userRepo) Delete(ctx context.Context, id string) error {
	// Hard delete.
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
