package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// NotificationHistoryRepository covers the notification_history table
// from migration 000064. The table is both the dispatcher audit log and
// the backing store for the in-app bell (unread rows + mark-read).
type NotificationHistoryRepository interface {
	Create(ctx context.Context, h *models.NotificationHistory) error

	// UpdateOutcome transitions a pending row to its terminal outcome
	// (sent/failed/skipped) after the dispatcher finishes sending. Used
	// both on success and on permanent-fail paths; retry_count should be
	// advanced by the caller before the call.
	UpdateOutcome(ctx context.Context, id, outcome, errMsg string, retryCount int) error

	// MarkRead stamps read_at=now for a single row. Used by the bell UI
	// when the admin expands a notification. Returns ErrNotFound if the
	// row doesn't exist; silent no-op if the row is already read.
	MarkRead(ctx context.Context, id string) error

	// MarkAllReadForUser stamps read_at=now for every unread row
	// targeted at the given user_id. Returns the number of rows it
	// updated so the UI can update its unread-badge count.
	MarkAllReadForUser(ctx context.Context, userID string) (int64, error)

	// DeleteAllForUser removes rows for the "Clear all" UI action.
	// Regular user: only rows where user_id = userID. Admin
	// (includeBroadcast=true): also removes user_id IS NULL broadcast
	// rows so system-wide events can be cleared from the admin inbox.
	DeleteAllForUser(ctx context.Context, userID string, includeBroadcast bool) (int64, error)

	FindByID(ctx context.Context, id string) (*models.NotificationHistory, error)

	// ListForUser returns recent rows for a user (bell dropdown). Order
	// is created_at DESC so the newest notifications surface first.
	ListForUser(ctx context.Context, userID string, opts ListOptions) ([]models.NotificationHistory, int64, error)

	// ListForAdminInbox returns rows targeted at the admin user plus
	// broadcast rows (user_id IS NULL). Used by the admin's bell
	// dropdown so system-wide events (disk full, service down) surface
	// alongside personalised ones without the UI making two calls.
	ListForAdminInbox(ctx context.Context, adminUserID string, opts ListOptions) ([]models.NotificationHistory, int64, error)

	// CountUnreadForAdminInbox mirrors ListForAdminInbox's row set for
	// the badge count.
	CountUnreadForAdminInbox(ctx context.Context, adminUserID string) (int64, error)

	// CountUnreadForUser is the bell-icon badge count. Uses the
	// idx_notification_history_user_read composite index.
	CountUnreadForUser(ctx context.Context, userID string) (int64, error)

	// ListRecentByEvent lets the dispatcher + alert-suppression logic
	// look at recent firings of an event_kind (e.g. "don't re-fire
	// disk.full.95 if we already fired it in the last 10 minutes").
	ListRecentByEvent(ctx context.Context, eventKind string, since time.Time) ([]models.NotificationHistory, error)
}

type notificationHistoryRepo struct{ db *gorm.DB }

func NewNotificationHistoryRepository(db *gorm.DB) NotificationHistoryRepository {
	return &notificationHistoryRepo{db: db}
}

func (r *notificationHistoryRepo) Create(ctx context.Context, h *models.NotificationHistory) error {
	return r.db.WithContext(ctx).Create(h).Error
}

func (r *notificationHistoryRepo) UpdateOutcome(ctx context.Context, id, outcome, errMsg string, retryCount int) error {
	res := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"outcome":       outcome,
			"error_message": errMsg,
			"retry_count":   retryCount,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *notificationHistoryRepo) MarkRead(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("id = ? AND read_at IS NULL", id).
		Update("read_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Either the row doesn't exist, or it's already read. Treat
		// the latter as success — the UI doesn't care either way. To
		// surface NotFound, re-check existence.
		var count int64
		if err := r.db.WithContext(ctx).
			Model(&models.NotificationHistory{}).
			Where("id = ?", id).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (r *notificationHistoryRepo) MarkAllReadForUser(ctx context.Context, userID string) (int64, error) {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", now)
	return res.RowsAffected, res.Error
}

func (r *notificationHistoryRepo) DeleteAllForUser(ctx context.Context, userID string, includeBroadcast bool) (int64, error) {
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if includeBroadcast {
		q = r.db.WithContext(ctx).Where("user_id = ? OR user_id IS NULL", userID)
	}
	res := q.Delete(&models.NotificationHistory{})
	return res.RowsAffected, res.Error
}

func (r *notificationHistoryRepo) FindByID(ctx context.Context, id string) (*models.NotificationHistory, error) {
	var row models.NotificationHistory
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (r *notificationHistoryRepo) ListForUser(ctx context.Context, userID string, opts ListOptions) ([]models.NotificationHistory, int64, error) {
	var rows []models.NotificationHistory
	var total int64
	base := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("user_id = ?", userID)
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q := base.Order("created_at DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *notificationHistoryRepo) ListForAdminInbox(ctx context.Context, adminUserID string, opts ListOptions) ([]models.NotificationHistory, int64, error) {
	var rows []models.NotificationHistory
	var total int64
	base := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("user_id = ? OR user_id IS NULL", adminUserID)
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q := base.Order("created_at DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	if opts.Offset > 0 {
		q = q.Offset(opts.Offset)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *notificationHistoryRepo) CountUnreadForAdminInbox(ctx context.Context, adminUserID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("(user_id = ? OR user_id IS NULL) AND read_at IS NULL", adminUserID).
		Count(&count).Error
	return count, err
}

func (r *notificationHistoryRepo) CountUnreadForUser(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&models.NotificationHistory{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Count(&count).Error
	return count, err
}

func (r *notificationHistoryRepo) ListRecentByEvent(ctx context.Context, eventKind string, since time.Time) ([]models.NotificationHistory, error) {
	var rows []models.NotificationHistory
	err := r.db.WithContext(ctx).
		Where("event_kind = ? AND created_at >= ?", eventKind, since).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}
