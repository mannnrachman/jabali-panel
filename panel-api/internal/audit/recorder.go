package audit

import (
	"context"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// auditCreator is the narrow slice of repository.AuditEventRepository
// the Recorder needs for its Redis-down fallback. Defined here (where
// it's used) so tests fake 1 method, not the whole repo (the
// narrow-interface convention).
type auditCreator interface {
	Create(ctx context.Context, e *models.AuditEvent) error
}

// publisher is the narrow slice of *AuditQueue the Recorder needs.
type publisher interface {
	Publish(ctx context.Context, e *models.AuditEvent) (string, error)
}

// Recorder is the one write path into the audit log. Record is
// fire-and-forget by contract: it MUST NOT block or fail the caller's
// request (the M44 BumpLastUsed discipline). The durable record's
// existence never depends on Redis.
type Recorder interface {
	Record(e *models.AuditEvent)
}

type recorder struct {
	q    publisher
	repo auditCreator
	log  *slog.Logger
}

// NewRecorder wires the recorder. q may be nil (Redis not configured /
// tests) — then every event goes straight to the DB fallback. repo
// must be non-nil (audit with no durable sink is pointless).
func NewRecorder(q publisher, repo auditCreator, log *slog.Logger) Recorder {
	if log == nil {
		log = slog.Default()
	}
	return &recorder{q: q, repo: repo, log: log}
}

// Record normalises the event (ULID id, UTC ts, default result) then
// hands it off on a detached goroutine: publish to jabali:audit:queue;
// on any publish failure (incl. q==nil) fall back to a direct
// repo.Create with NULL hashes (the Consumer back-fills the chain on
// recovery). A detached context (not the request ctx, which may be
// cancelled the instant the handler returns) bounds the work.
func (r *recorder) Record(e *models.AuditEvent) {
	if e == nil {
		return
	}
	if e.ID == "" {
		e.ID = ids.NewULID()
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if e.Result == "" {
		e.Result = models.AuditResultOK
	}
	if e.ActorKind == "" {
		e.ActorKind = models.AuditActorSystem
	}

	go func(ev *models.AuditEvent) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if r.q != nil {
			if _, err := r.q.Publish(ctx, ev); err == nil {
				return // sealed by the chain consumer
			} else {
				r.log.Warn("audit: stream publish failed; using DB fallback",
					"id", ev.ID, "action", ev.Action, "err", err)
			}
		}
		// Fallback: persist now with NULL hashes; the Consumer's
		// startup/periodic sweep chains it via SetHashes. Audit is
		// fail-open-but-recorded.
		if err := r.repo.Create(ctx, ev); err != nil {
			// Last resort: nothing left but the log. An audit event
			// that can't be persisted is a real (but rare) gap; make
			// it loud, never silent.
			r.log.Error("audit: DB fallback persist failed — EVENT LOST",
				"id", ev.ID, "actor_kind", ev.ActorKind, "action", ev.Action,
				"result", ev.Result, "err", err)
		}
	}(e)
}
