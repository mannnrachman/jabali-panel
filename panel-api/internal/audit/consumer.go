package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// chainStore is the narrow repo slice the single-writer Consumer
// needs. (repository.AuditEventRepository satisfies it.)
type chainStore interface {
	Create(ctx context.Context, e *models.AuditEvent) error
	LatestRowHash(ctx context.Context) (string, error)
	SetHashes(ctx context.Context, id, prevHash, rowHash string) error
	ListUnsealed(ctx context.Context, limit int) ([]models.AuditEvent, error)
}

// ErrSealed is returned by SetHashes (via ErrNotFound) when a row is
// already sealed; the sweep treats it as "someone else chained it" —
// benign, skip.
var errSweepSkip = errors.New("audit: row already sealed")

// computeRowHash is the chain link: hex(sha256(prevHash || canonical)).
// Deterministic — `jabali audit verify` recomputes with the same fn.
func computeRowHash(prevHash string, e *models.AuditEvent) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write([]byte("\x1e")) // record separator: prev can't bleed into canonical
	h.Write([]byte(canonical(e)))
	return hex.EncodeToString(h.Sum(nil))
}

// Consumer is the SINGLE writer of the hash chain. Exactly one runs
// (one consumer name, batch=1, strictly sequential) so PrevHash->
// RowHash stays a total order. The chain head lives in the DB
// (LatestRowHash), so a restart resumes correctly.
type Consumer struct {
	q     *AuditQueue
	store chainStore
	log   *slog.Logger
	name  string
}

func NewConsumer(q *AuditQueue, store chainStore, log *slog.Logger) *Consumer {
	if log == nil {
		log = slog.Default()
	}
	return &Consumer{q: q, store: store, log: log, name: "audit-chain-1"}
}

// seal computes + assigns PrevHash/RowHash for e given the current
// chain head. PrevHash is nil at genesis (no sealed row yet).
func (c *Consumer) seal(ctx context.Context, e *models.AuditEvent) error {
	prev, err := c.store.LatestRowHash(ctx)
	if err != nil {
		return err
	}
	row := computeRowHash(prev, e)
	if prev != "" {
		e.PrevHash = &prev
	}
	e.RowHash = &row
	return nil
}

// sweepFallback chains rows the Redis-down fallback inserted with NULL
// hashes (oldest first, so the chain stays time-ordered). Runs at
// startup and on every idle tick (catches Redis recovery mid-run).
func (c *Consumer) sweepFallback(ctx context.Context) {
	rows, err := c.store.ListUnsealed(ctx, 200)
	if err != nil {
		c.log.Warn("audit: sweep ListUnsealed failed", "err", err)
		return
	}
	for i := range rows {
		e := &rows[i]
		prev, err := c.store.LatestRowHash(ctx)
		if err != nil {
			c.log.Warn("audit: sweep LatestRowHash failed", "err", err)
			return
		}
		row := computeRowHash(prev, e)
		if err := c.store.SetHashes(ctx, e.ID, prev, row); err != nil {
			// ErrNotFound here = already sealed by a prior pass;
			// benign, continue.
			continue
		}
	}
}

// Start runs until ctx is done: ensure group, sweep any fallback
// backlog, then consume + seal one event at a time.
func (c *Consumer) Start(ctx context.Context) error {
	if err := c.q.EnsureGroup(ctx); err != nil {
		return err
	}
	c.sweepFallback(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		streams, err := c.q.Read(ctx, c.name, 1, 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.log.Warn("audit: stream read failed; retrying", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(streams) == 0 {
			c.sweepFallback(ctx) // idle → catch any Redis-recovery backlog
			continue
		}
		for _, st := range streams {
			for _, msg := range st.Messages {
				e := parseStream(msg.Values)
				if err := c.seal(ctx, e); err != nil {
					c.log.Warn("audit: seal failed; leaving unacked for retry",
						"id", e.ID, "err", err)
					continue // no Ack → redelivered
				}
				if err := c.store.Create(ctx, e); err != nil {
					c.log.Warn("audit: chained insert failed; leaving unacked",
						"id", e.ID, "err", err)
					continue
				}
				if err := c.q.Ack(ctx, msg.ID); err != nil {
					c.log.Warn("audit: ack failed (row persisted; will redeliver)",
						"id", e.ID, "stream_id", msg.ID, "err", err)
				}
			}
		}
	}
}

var _ = errSweepSkip // reserved for an explicit-skip refinement
