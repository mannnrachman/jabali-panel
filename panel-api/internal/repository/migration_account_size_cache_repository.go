package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// MigrationAccountSizeCacheRepository persists per-account size hits
// keyed by (host, source_user). TTL applied at read time — repo
// returns ErrNotFound if the cached row is stale, callers re-discover.
//
// ADR-0095 decision 6.
type MigrationAccountSizeCacheRepository interface {
	Get(ctx context.Context, host, user string, ttl time.Duration) (*models.MigrationAccountSizeCache, error)
	Upsert(ctx context.Context, host, user string, sizeBytes int64) error
}

type migrationAccountSizeCacheRepo struct{ db *gorm.DB }

func NewMigrationAccountSizeCacheRepository(db *gorm.DB) MigrationAccountSizeCacheRepository {
	return &migrationAccountSizeCacheRepo{db: db}
}

func (r *migrationAccountSizeCacheRepo) Get(ctx context.Context, host, user string, ttl time.Duration) (*models.MigrationAccountSizeCache, error) {
	var row models.MigrationAccountSizeCache
	err := r.db.WithContext(ctx).
		Where("host = ? AND source_user = ?", host, user).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if time.Since(row.FetchedAt) > ttl {
		return nil, ErrNotFound
	}
	return &row, nil
}

func (r *migrationAccountSizeCacheRepo) Upsert(ctx context.Context, host, user string, sizeBytes int64) error {
	now := time.Now().UTC()
	row := &models.MigrationAccountSizeCache{
		Host:       host,
		SourceUser: user,
		SizeBytes:  sizeBytes,
		FetchedAt:  now,
	}
	return r.db.WithContext(ctx).
		Where("host = ? AND source_user = ?", host, user).
		Assign(map[string]any{"size_bytes": sizeBytes, "fetched_at": now}).
		FirstOrCreate(row).Error
}
