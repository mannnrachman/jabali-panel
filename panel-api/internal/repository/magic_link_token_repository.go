package repository

import (
	"context"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MagicLinkTokenRepository wraps the magic_link_tokens table.
//
// MarkUsed is the security-critical entry point: it serialises
// concurrent validates with `SELECT … FOR UPDATE NOWAIT` and only
// returns nil when this caller atomically transitioned the row from
// unused → used. See ADR-0039 §2 for the threat model.
type MagicLinkTokenRepository interface {
	// Create persists a freshly minted token. Caller has already
	// computed TokenHash = SHA256(token_id) and ExpiresAt = mint+60s.
	Create(ctx context.Context, t *models.MagicLinkToken) error

	// FindByTokenHash returns the row whose TokenHash matches.
	// Returns ErrNotFound if no row exists. Does NOT check used_at
	// or expires_at — the caller does those (MarkUsed handles
	// used_at, MagicLinkToken.IsExpiredAt handles expires_at).
	FindByTokenHash(ctx context.Context, hash string) (*models.MagicLinkToken, error)

	// MarkUsed transitions the row from unused → used atomically.
	// Returns:
	//   nil            on success (caller may proceed to issue cookie)
	//   ErrLocked      if another validate holds the row's lock
	//                  (handler maps to HTTP 429)
	//   ErrAlreadyUsed if the row was already consumed
	//                  (handler maps to HTTP 410)
	//   ErrNotFound    if no row with that id exists
	//                  (handler maps to HTTP 404)
	MarkUsed(ctx context.Context, id string) error

	// DeleteExpired removes rows whose expires_at is in the past.
	// Returns the number of rows deleted. Used by the reconciler's
	// periodic janitor sweep to keep the table small.
	DeleteExpired(ctx context.Context) (int64, error)
}

type magicLinkTokenRepo struct{ db *gorm.DB }

// NewMagicLinkTokenRepository returns a GORM-backed implementation.
func NewMagicLinkTokenRepository(db *gorm.DB) MagicLinkTokenRepository {
	return &magicLinkTokenRepo{db: db}
}

func (r *magicLinkTokenRepo) Create(ctx context.Context, t *models.MagicLinkToken) error {
	if err := r.db.WithContext(ctx).Create(t).Error; err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (r *magicLinkTokenRepo) FindByTokenHash(ctx context.Context, hash string) (*models.MagicLinkToken, error) {
	var row models.MagicLinkToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", hash).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// MariaDB and MySQL 8 both return error number 3572 when a
// `SELECT ... FOR UPDATE NOWAIT` cannot acquire the row lock.
// Using a single constant for both engines.
const lockNoWaitErrno uint16 = 3572

func (r *magicLinkTokenRepo) MarkUsed(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.MagicLinkToken
		err := tx.
			Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).
			Where("id = ?", id).
			First(&row).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == uint16(lockNoWaitErrno) {
				return ErrLocked
			}
			return err
		}
		if row.UsedAt != nil {
			return ErrAlreadyUsed
		}

		// Belt-and-suspenders CAS: even with FOR UPDATE NOWAIT giving
		// us serialisation, the UPDATE includes `WHERE used_at IS NULL`
		// so a separately-running janitor or admin tool that flipped
		// used_at outside this transaction can't be silently overwritten.
		res := tx.Exec(
			"UPDATE magic_link_tokens SET used_at = NOW(6) WHERE id = ? AND used_at IS NULL",
			id,
		)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrAlreadyUsed
		}
		return nil
	})
}

func (r *magicLinkTokenRepo) DeleteExpired(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("expires_at <= ?", time.Now()).
		Delete(&models.MagicLinkToken{})
	return res.RowsAffected, res.Error
}
