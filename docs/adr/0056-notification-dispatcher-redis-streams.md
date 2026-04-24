# ADR-0056: M14 — Notification dispatcher via Redis Streams + consumer group

**Status:** Accepted (2026-04-24)
**Driven by:** Plan `plans/m14-notifications.md` (9-step construction plan, Step 1 schema + Redis install, Step 2 dispatcher rewrite). Supersedes the earlier in-process buffered-channel draft that shipped briefly in the plan before the Redis install was folded into Step 1.

## Context

System events from the panel (domain expiry, cert renewal outcome, disk-full, service down, backup failure, CrowdSec ban-rate spikes) must fan out to:

- External channels an admin configured (email / Slack / Discord / ntfy / generic webhook / Web Push).
- An in-app bell dropdown for the logged-in admin.

Two properties the dispatcher must have:

1. **Restart-safe.** A notification enqueued just before `systemctl restart jabali-panel` must still fire after the restart completes. "Domain expires in 24 h" dropping on the floor because of a daytime restart is a silent failure class we have already burned on (see `feedback_migration_data_seed_ordering.md` for the general pattern — invariants that disappear when the process restarts).
2. **Observable in-flight.** If a channel sender (say the Slack webhook POST) blocks for 30 s because Slack is rate-limiting, the dispatcher needs to know a specific envelope is in-flight, by which consumer, for how long — and reclaim it if the consumer dies.

The earlier draft of this ADR had the dispatcher as a single goroutine reading from an in-memory buffered channel (`chan NotificationEnvelope`, cap 1024). That made property (1) impossible: restart = queue lost. It also made (2) hard: in-flight state was a goroutine stack, not inspectable.

A third external force: the operator wants Redis in the stack anyway (planned WordPress object-cache, future CrowdSec bouncer), and Memory `feedback_install_sh_is_truth.md` says install-time provisioning beats "add Redis later" milestones. So the cost of adding Redis *now* is amortised against future uses; the dispatcher gets queue persistence for free.

## Decision

The notification dispatcher is an in-process consumer of a Redis Stream. One Redis key per panel-api process, shared consumer group, reclaim goroutine for stuck entries, dead-letter stream for permanent failures.

### Architecture

**Keys (all in `db 0` — per ADR-0059 Redis is shared with future WP cache in `db 1`):**

| Key                     | Type   | Purpose                                                                 |
| ----------------------- | ------ | ----------------------------------------------------------------------- |
| `jabali:notif:stream`   | STREAM | Primary queue. One entry per `NotificationEnvelope`.                    |
| `jabali:notif:dlq`      | STREAM | Dead-letter queue. Entries that exhausted retry budget.                 |
| *(consumer group)*      | —      | Name `notif-dispatcher`, created lazily via `XGROUP CREATE MKSTREAM`.   |

Stream entries carry the full envelope JSON as a single field (`payload`), plus a `ulid` top-level field (matching `notification_history.id`) so the consumer can correlate a stream entry with its history row without parsing JSON first.

**Consumer loop (one goroutine per panel-api process, named `notif-dispatcher-<hostname>-<pid>`):**

```go
for {
    entries, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
        Group:    "notif-dispatcher",
        Consumer: consumerName,
        Streams:  []string{"jabali:notif:stream", ">"},
        Count:    16,
        Block:    5 * time.Second,
    }).Result()
    // for each entry: look up enabled channels, fan out, XACK on success
    // on sender error: don't XACK — pending-entry-list (PEL) keeps it
    // retry policy lives in the sender, not here
}
```

**Reclaim loop (second goroutine, every 60 s):**

```go
// Use XPENDING to find entries idle > 2*consumer-tick (10 s)
// XCLAIM them to the current consumer, re-process
// If delivery count > 5 → XADD to DLQ stream + XACK from main
```

This is the single mechanism that handles consumer death (panel-api OOM-killed mid-fanout) AND stuck external calls (Slack webhook hanging): both leave an entry in the pending-entry-list, the reclaim loop notices idleness, and the reprocess happens in a different consumer goroutine.

**DLQ handling:** operator-only. DLQ entries are listed by a new admin endpoint (planned for Step 5) but never auto-retried. An entry is in DLQ because we've retried it 5× and each attempt failed — automated re-retry would just thrash.

**No in-memory fallback.** If Redis is unreachable at boot, `serve.go` fails fast (`_die "Redis unreachable at %s: %v"`). There is no "degraded mode" where the panel runs without the dispatcher — the consequence (silent event loss) is worse than the consequence of refusing to start. The systemd unit has `Requires=redis-server.service` + `After=redis-server.service` so restart ordering is right on fresh boots.

### Trade-offs on the retry shape

Each channel sender owns its own retry loop (exponential backoff, max 3 attempts in-loop) before surrendering the envelope back to the stream. Reasoning:

- In-loop retries for a 503 from Slack (transient) don't cost dispatcher throughput — the consumer is blocked on that one envelope anyway, and XCLAIM would just move it to another consumer that also hits Slack.
- Stream-level retries (via PEL + reclaim) handle the case where the *consumer* crashed, not where the downstream is flaky. Conflating them leaks fake retries — an envelope that was almost-sent (in-loop on attempt 3/3) then got reclaimed and tried again from scratch.

Permanent-failure signal is an error wrapped in `senders.ErrPermanent` — the dispatcher XADDs to DLQ + XACKs without re-queueing, even if delivery-count is low (e.g. 404 on a webhook URL the operator just mistyped).

## Alternatives considered

**In-memory buffered channel (the earlier draft).** Rejected: no restart persistence. Would have required a separate "pending-history re-queue on boot" path that walks `notification_history` for `outcome='pending'`, which races with anything the dispatcher finished between history-write and outcome-update. Redis Streams remove the race entirely — an entry is in the stream xor it's been XACKed.

**RabbitMQ / NATS / dedicated MQ.** Rejected: install footprint is disproportionate for a handful of notifications/day on a single-node panel. Redis was already wanted for WP object-cache; piggyback.

**BoltDB file-backed queue (an even earlier draft).** Rejected: we'd be writing our own consumer-group + XCLAIM semantics from scratch. Redis ships them, proven, with `go-redis/v9` as the client library the WP cache will reuse.

**Write straight to `notification_history` + reconciler-style poller.** Rejected: reconciler runs every N seconds; response latency for a critical disk-full alert would be N/2 on average. Redis push gives sub-second fanout.

## Consequences

**Positive:**
- Restart-safe queue. Enqueue-before-restart-fires-after-restart works by construction; no re-queue path needed.
- In-flight visibility via `XPENDING` — admin API can surface "5 entries stuck for >5 min" without instrumentation.
- Same Redis powers the future WP object-cache (`db 1`), so install.sh gains one package, two users.
- Sender retry loops stay local to the sender — no shared retry framework to maintain.

**Negative:**
- panel-api now has a hard runtime dep on `redis-server.service`. If Redis is OOM-killed, the panel stops emitting notifications until it comes back (with systemd auto-restart, seconds). Mitigation: `Requires=` guarantees the right shutdown/startup order; `appendonly yes` in the Redis config means Redis itself recovers queued entries from AOF after its own restart.
- DLQ grows unbounded unless operators inspect + prune. Step 5 exposes a clear-DLQ endpoint; runbook documents when to use it.
- Consumer name (`notif-dispatcher-<hostname>-<pid>`) changes every restart, so PEL entries from the previous incarnation need reclaim by the new consumer. The 60 s reclaim loop catches them within one tick post-restart.

## Related

- Plan: `plans/m14-notifications.md` (Step 2 defines the consumer + reclaim implementation)
- Code (Step 2): `panel-api/internal/notif/dispatcher.go`, `panel-api/internal/notif/senders/*.go`
- ADR-0059: Redis as shared local cache/queue (install + socket security model)
- ADR-0050: M25 unix-socket hardening — Redis inherits the same `jabali-sockets` group + 0660 model
- ADR-0002: DB as truth — history rows in MariaDB remain the audit log; Redis is transport
