// Package audit is the M49 unified audit log core (ADR-0105).
//
// Shape mirrors the M14 notification dispatcher (a Redis stream + an
// in-process single-writer consumer) but on its OWN wire — a full
// structured audit record, NOT a notifications.Envelope (which is
// notification-shaped and whose wire is documented as breaking to
// extend; see the 2026-05-17 design correction in ADR-0105).
//
// Flow: Recorder.Record -> XADD jabali:audit:queue (async,
// best-effort) -> single-writer Consumer computes PrevHash->RowHash
// and Create()s the row. Redis-down: Recorder falls back to a direct
// repo.Create with NULL hashes (off the request goroutine); the
// Consumer back-fills those on recovery via repo.SetHashes. The audit
// record's existence NEVER depends on Redis (fail-open-but-recorded),
// in deliberate contrast to M44's fail-closed replay store.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Stream + consumer-group names. Public so tests + the producer path
// target the same keys without mis-typing. Distinct from the M14
// stream — audit is its own pipeline.
const (
	StreamAudit   = "jabali:audit:queue"
	ConsumerGroup = "audit-chain"
)

// field keys for the stream record (producer + consumer locked
// together — any rename is a breaking wire change).
const (
	fID       = "id"
	fTSNano   = "ts_nano"
	fActorUID = "actor_user_id"
	fActorK   = "actor_kind"
	fSubjUID  = "subject_user_id"
	fAction   = "action"
	fTgtType  = "target_type"
	fTgtID    = "target_id"
	fResult   = "result"
	fSrcIP    = "source_ip"
	fReqID    = "request_id"
	fMeta     = "meta"
)

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

// streamMap serialises an AuditEvent to the Redis field map. Nullable
// columns use "" as the absent sentinel; meta is the raw JSON string.
func streamMap(e *models.AuditEvent) map[string]any {
	meta := ""
	if len(e.Meta) > 0 {
		meta = string(e.Meta)
	}
	return map[string]any{
		fID:       e.ID,
		fTSNano:   strconv.FormatInt(e.TS.UTC().UnixNano(), 10),
		fActorUID: deref(e.ActorUserID),
		fActorK:   e.ActorKind,
		fSubjUID:  deref(e.SubjectUserID),
		fAction:   e.Action,
		fTgtType:  e.TargetType,
		fTgtID:    e.TargetID,
		fResult:   e.Result,
		fSrcIP:    deref(e.SourceIP),
		fReqID:    deref(e.RequestID),
		fMeta:     meta,
	}
}

// parseStream reverses streamMap from a redis.XMessage's Values.
func parseStream(vals map[string]any) *models.AuditEvent {
	get := func(k string) string {
		if v, ok := vals[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	ts := time.Now().UTC()
	if n, err := strconv.ParseInt(get(fTSNano), 10, 64); err == nil {
		ts = time.Unix(0, n).UTC()
	}
	var meta json.RawMessage
	if m := get(fMeta); m != "" {
		meta = json.RawMessage(m)
	}
	return &models.AuditEvent{
		ID:            get(fID),
		TS:            ts,
		ActorUserID:   ptrIfSet(get(fActorUID)),
		ActorKind:     get(fActorK),
		SubjectUserID: ptrIfSet(get(fSubjUID)),
		Action:        get(fAction),
		TargetType:    get(fTgtType),
		TargetID:      get(fTgtID),
		Result:        get(fResult),
		SourceIP:      ptrIfSet(get(fSrcIP)),
		RequestID:     ptrIfSet(get(fReqID)),
		Meta:          meta,
	}
}

// canonical is the stable byte string the hash chain signs. Field
// order is fixed; nullable fields use the "" sentinel. Deterministic
// (no map iteration) so `jabali audit verify` can recompute.
func canonical(e *models.AuditEvent) string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	w(e.ID)
	w(strconv.FormatInt(e.TS.UTC().UnixNano(), 10))
	w(deref(e.ActorUserID))
	w(e.ActorKind)
	w(deref(e.SubjectUserID))
	w(e.Action)
	w(e.TargetType)
	w(e.TargetID)
	w(e.Result)
	w(deref(e.SourceIP))
	w(deref(e.RequestID))
	if len(e.Meta) > 0 {
		w(string(e.Meta))
	} else {
		w("")
	}
	return b.String()
}

// AuditQueue wraps the Redis client with the audit-stream ops the
// Recorder + Consumer need. Mirrors notifications.Queue.
type AuditQueue struct {
	rdb *redis.Client
}

func NewAuditQueue(rdb *redis.Client) *AuditQueue { return &AuditQueue{rdb: rdb} }

// EnsureGroup creates the consumer group at the stream tail
// (MKSTREAM), swallowing BUSYGROUP (idempotent on restart).
func (q *AuditQueue) EnsureGroup(ctx context.Context) error {
	err := q.rdb.XGroupCreateMkStream(ctx, StreamAudit, ConsumerGroup, "$").Err()
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

// Publish XADDs one event. Returns the Redis-assigned id.
func (q *AuditQueue) Publish(ctx context.Context, e *models.AuditEvent) (string, error) {
	return q.rdb.XAdd(ctx, &redis.XAddArgs{Stream: StreamAudit, Values: streamMap(e)}).Result()
}

// Read blocks up to block for the next batch from the group. Returns
// nil on BLOCK timeout (redis.Nil) — a normal "no work" iteration.
func (q *AuditQueue) Read(ctx context.Context, consumer string, batch int, block time.Duration) ([]redis.XStream, error) {
	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    ConsumerGroup,
		Consumer: consumer,
		Streams:  []string{StreamAudit, ">"},
		Count:    int64(batch),
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return res, err
}

// Ack acknowledges processed stream ids.
func (q *AuditQueue) Ack(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return q.rdb.XAck(ctx, StreamAudit, ConsumerGroup, ids...).Err()
}
