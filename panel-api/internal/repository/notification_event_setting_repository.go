package repository

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// NotificationEventSettingRepository covers the per-event-kind
// enable toggle table. Read path is hot — dispatcher consults it on
// every Publish — so the implementation caches the full row set
// in-process and refreshes lazily on each Set or after a TTL.
type NotificationEventSettingRepository interface {
	List(ctx context.Context) ([]models.NotificationEventSetting, error)
	IsEnabled(ctx context.Context, eventKind string) (bool, error)
	Set(ctx context.Context, eventKind string, enabled bool) error
	EnsureDefaults(ctx context.Context) (seeded int, err error)
}

const eventSettingCacheTTL = 30 * time.Second

type notificationEventSettingRepo struct {
	db *gorm.DB

	mu       sync.RWMutex
	cache    map[string]bool
	cachedAt atomic.Int64
}

func NewNotificationEventSettingRepository(db *gorm.DB) NotificationEventSettingRepository {
	return &notificationEventSettingRepo{db: db, cache: make(map[string]bool)}
}

func (r *notificationEventSettingRepo) List(ctx context.Context) ([]models.NotificationEventSetting, error) {
	var rows []models.NotificationEventSetting
	if err := r.db.WithContext(ctx).Order("event_kind ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *notificationEventSettingRepo) refreshCache(ctx context.Context) error {
	rows, err := r.List(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]bool, len(rows))
	for _, row := range rows {
		next[row.EventKind] = row.Enabled
	}
	r.mu.Lock()
	r.cache = next
	r.mu.Unlock()
	r.cachedAt.Store(time.Now().UnixNano())
	return nil
}

func (r *notificationEventSettingRepo) IsEnabled(ctx context.Context, eventKind string) (bool, error) {
	last := r.cachedAt.Load()
	if last == 0 || time.Since(time.Unix(0, last)) > eventSettingCacheTTL {
		if err := r.refreshCache(ctx); err != nil {
			return false, err
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.cache[eventKind]; ok {
		return v, nil
	}
	// Unknown kind — fall back to the meta table's default-on flag
	// so an event source firing a not-yet-seeded kind still works
	// before EnsureDefaults catches up on the next boot.
	if meta := models.LookupNotificationEventKind(eventKind); meta != nil {
		return meta.DefaultOn, nil
	}
	// Truly unknown — let it pass; old call sites firing legacy kinds
	// are better surfaced in the inbox than silently dropped.
	return true, nil
}

func (r *notificationEventSettingRepo) Set(ctx context.Context, eventKind string, enabled bool) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.NotificationEventSetting{}).
		Where("event_kind = ?", eventKind).
		Updates(map[string]any{"enabled": enabled, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		row := models.NotificationEventSetting{
			EventKind: eventKind, Enabled: enabled, UpdatedAt: now,
		}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			return err
		}
	}
	// Invalidate cache so the next IsEnabled hits DB.
	r.cachedAt.Store(0)
	return nil
}

func (r *notificationEventSettingRepo) EnsureDefaults(ctx context.Context) (int, error) {
	var existing []string
	if err := r.db.WithContext(ctx).
		Model(&models.NotificationEventSetting{}).
		Pluck("event_kind", &existing).Error; err != nil {
		return 0, err
	}
	have := make(map[string]struct{}, len(existing))
	for _, k := range existing {
		have[k] = struct{}{}
	}
	now := time.Now().UTC()
	seeded := 0
	for _, meta := range models.AllNotificationEventKinds {
		if _, ok := have[meta.Kind]; ok {
			continue
		}
		row := models.NotificationEventSetting{
			EventKind: meta.Kind, Enabled: meta.DefaultOn, UpdatedAt: now,
		}
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				continue
			}
			return seeded, err
		}
		seeded++
	}
	r.cachedAt.Store(0)
	return seeded, nil
}
