package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// AuditEventRepository covers the append-only audit_events table
// (migration 000137, ADR-0105, M49). Deliberately exposes NO Update or
// Delete of event content — append-only is enforced by the absence of
// a mutation path. The single controlled exception is SetHashes, which
// the chain consumer uses to back-fill the hash columns of a row the
// Redis-down DB fallback inserted with NULLs; it is gated server-side
// with `row_hash IS NULL` so a sealed row can never be rewritten.
//
// ListBySubject is the per-user /me/activity scope: it is the ONLY
// way the user view reads, and the subject filter is applied here
// (server-side), never via a client parameter — the IDOR scar.
type AuditEventRepository interface {
	// Create appends one event. Used by the chain consumer (the
	// authoritative writer) and by the Redis-down DB fallback.
	Create(ctx context.Context, e *models.AuditEvent) error

	FindByID(ctx context.Context, id string) (*models.AuditEvent, error)

	// ListAll is the admin forensics view (every row, raw).
	ListAll(ctx context.Context, opts ListOptions) ([]models.AuditEvent, int64, error)

	// ListBySubject is the per-user view. subjectUserID is the
	// session identity, resolved server-side by the handler — NEVER a
	// client filter. Rows with a NULL subject_user_id are structurally
	// excluded (safe-fail), so server-internal events never leak into
	// a user's activity feed.
	ListBySubject(ctx context.Context, subjectUserID string, opts ListOptions) ([]models.AuditEvent, int64, error)

	// LatestRowHash returns the row_hash of the most recent sealed
	// (chained) row, or "" when the chain has no sealed row yet
	// (genesis). The chain consumer feeds this in as the next row's
	// prev_hash.
	LatestRowHash(ctx context.Context) (string, error)

	// SetHashes back-fills prev_hash/row_hash for a fallback row that
	// was inserted with NULL hashes. Gated with `row_hash IS NULL`:
	// returns ErrNotFound (effectively "already sealed / not found")
	// rather than ever overwriting a sealed row. Consumer-only.
	SetHashes(ctx context.Context, id, prevHash, rowHash string) error

	// ListUnsealed returns rows with a NULL row_hash (inserted by the
	// recorder's Redis-down DB fallback), oldest-first and capped at
	// limit — the chain consumer's back-fill work queue. Read-only;
	// append-only-safe.
	ListUnsealed(ctx context.Context, limit int) ([]models.AuditEvent, error)
}

// Column allowlists for the audit_events list views. Empty-key-proof
// (see ListCols doc): Sort is whitelist-matched, so Sort/Search names
// can't be an injection vector.
var auditEventListCols = ListCols{
	Search:      []string{"action", "target_id", "actor_kind", "result"},
	Sort:        []string{"ts", "action", "actor_kind", "result", "actor_user_id", "subject_user_id"},
	DefaultSort: "ts",
}

type auditEventRepo struct{ db *gorm.DB }

func NewAuditEventRepository(db *gorm.DB) AuditEventRepository {
	return &auditEventRepo{db: db}
}

func (r *auditEventRepo) Create(ctx context.Context, e *models.AuditEvent) error {
	if err := r.db.WithContext(ctx).Create(e).Error; err != nil {
		return err
	}
	return nil
}

func (r *auditEventRepo) FindByID(ctx context.Context, id string) (*models.AuditEvent, error) {
	var e models.AuditEvent
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *auditEventRepo) ListAll(ctx context.Context, opts ListOptions) ([]models.AuditEvent, int64, error) {
	var (
		rows  []models.AuditEvent
		total int64
	)
	base := r.db.WithContext(ctx).Model(&models.AuditEvent{})

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, auditEventListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, auditEventListCols)
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *auditEventRepo) ListBySubject(ctx context.Context, subjectUserID string, opts ListOptions) ([]models.AuditEvent, int64, error) {
	var (
		rows  []models.AuditEvent
		total int64
	)
	// Subject scope applied HERE, server-side, from the session
	// identity — never a client-supplied filter (IDOR scar). A blank
	// subjectUserID would match no rows (safe-fail) rather than
	// returning everything.
	base := r.db.WithContext(ctx).Model(&models.AuditEvent{}).Where("subject_user_id = ?", subjectUserID)

	countQ := applyListOptions(base.Session(&gorm.Session{}), ListOptions{Search: opts.Search}, auditEventListCols)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if opts.Sort == "" && opts.Order == "" {
		opts.Order = "desc"
	}
	q := applyListOptions(base.Session(&gorm.Session{}), opts, auditEventListCols)
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *auditEventRepo) LatestRowHash(ctx context.Context) (string, error) {
	var e models.AuditEvent
	err := r.db.WithContext(ctx).
		Where("row_hash IS NOT NULL").
		Order("ts DESC, id DESC").
		Limit(1).
		First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil // genesis: no sealed row yet
		}
		return "", err
	}
	if e.RowHash == nil {
		return "", nil
	}
	return *e.RowHash, nil
}

func (r *auditEventRepo) SetHashes(ctx context.Context, id, prevHash, rowHash string) error {
	res := r.db.WithContext(ctx).
		Model(&models.AuditEvent{}).
		Where("id = ? AND row_hash IS NULL", id).
		Updates(map[string]any{"prev_hash": prevHash, "row_hash": rowHash})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Either no such id, or it is already sealed — both mean
		// "nothing to back-fill"; never overwrite a sealed row.
		return ErrNotFound
	}
	return nil
}

func (r *auditEventRepo) ListUnsealed(ctx context.Context, limit int) ([]models.AuditEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows []models.AuditEvent
	err := r.db.WithContext(ctx).
		Where("row_hash IS NULL").
		Order("ts ASC, id ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
