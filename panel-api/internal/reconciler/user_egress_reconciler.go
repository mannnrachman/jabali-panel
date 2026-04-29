package reconciler

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// userEgressLastSeen tracks the last counter value the reconciler read
// for each user. Used to compute per-tick deltas after a `nft list
// counters` (without reset). Process-local — survives across ticks but
// not across panel-api restart; on restart we re-baseline from the
// next tick's read. The M14 burst-source threshold compares against
// the per-tick delta, so a missed reset adds at most one tick of noise.
var (
	userEgressMu       sync.Mutex
	userEgressLastSeen = map[string]uint64{}
)

// reconcileUserEgress is the M34 per-tick pass. Reads every policy +
// dispatches user.egress.apply with the snapshot, then reads counters
// (without reset — RESET would race with a runaway-loop user dropping
// 1000s/sec) and persists per-tick delta as drop_count_24h. The column
// name is historical; the value is "drops since last tick" which is
// what the burst-source signal cares about.
func (r *Reconciler) reconcileUserEgress(ctx context.Context) {
	if r.userEgressPolicies == nil {
		return
	}
	policies, err := r.userEgressPolicies.ListAllForReconcile(ctx)
	if err != nil {
		r.log.Warn("user-egress reconcile: list policies", "error", err)
		return
	}
	if len(policies) == 0 {
		// No policies yet — emit an empty table so any prior table on
		// disk gets cleared. This costs one nft reload per tick on a
		// fresh install but keeps state convergent (DB is truth).
		r.applyUserEgress(ctx, nil)
		return
	}
	r.applyUserEgress(ctx, policies)
	r.readUserEgressCounters(ctx)
}

func (r *Reconciler) applyUserEgress(ctx context.Context, policies []repository.PolicyForReconcile) {
	users := make([]map[string]any, 0, len(policies))
	for _, p := range policies {
		// Defence in depth: the agent rejects unknown states.
		if p.State != models.UserEgressStateOff &&
			p.State != models.UserEgressStateLearning &&
			p.State != models.UserEgressStateEnforced {
			continue
		}
		extras := make([]map[string]any, 0, len(p.AllowedExtra))
		for _, e := range p.AllowedExtra {
			row := map[string]any{
				"cidr":     e.CIDR,
				"protocol": e.Protocol,
				"comment":  e.Comment,
			}
			if e.Port != nil {
				row["port"] = *e.Port
			}
			extras = append(extras, row)
		}
		users = append(users, map[string]any{
			"username":      p.Username,
			"state":         p.State,
			"allowed_extra": extras,
		})
	}
	dispatchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, agentErr := r.agent.Call(dispatchCtx, "user.egress.apply", map[string]any{
		"users": users,
	})
	if agentErr != nil {
		r.log.Warn("user-egress reconcile: agent apply failed", "error", agentErr,
			"user_count", len(users))
		return
	}
}

func (r *Reconciler) readUserEgressCounters(ctx context.Context) {
	dispatchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, agentErr := r.agent.Call(dispatchCtx, "user.egress.read_counters", map[string]any{
		"reset": false,
	})
	if agentErr != nil {
		r.log.Debug("user-egress reconcile: read_counters failed", "error", agentErr)
		return
	}
	var resp struct {
		Counters []struct {
			Username string `json:"username"`
			Packets  uint64 `json:"packets"`
		} `json:"counters"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		r.log.Warn("user-egress reconcile: counter parse failed", "error", err)
		return
	}

	now := time.Now().UTC()
	userEgressMu.Lock()
	defer userEgressMu.Unlock()
	for _, c := range resp.Counters {
		prev := userEgressLastSeen[c.Username]
		var delta uint64
		if c.Packets >= prev {
			delta = c.Packets - prev
		} else {
			// nft counter wrapped (extremely unlikely) or table was
			// rebuilt — treat the new value as the delta.
			delta = c.Packets
		}
		userEgressLastSeen[c.Username] = c.Packets
		// Resolve user_id by username via the policies list. We did not
		// thread the join through to the counters map for simplicity,
		// so we fetch the policies once and look up by username. The
		// list is bounded by the user count and read once per tick.
		if uid := r.lookupUserIDForEgress(ctx, c.Username); uid != "" {
			if err := r.userEgressPolicies.SetDropCount(ctx, uid, delta, now); err != nil {
				r.log.Debug("user-egress reconcile: SetDropCount failed",
					"user", c.Username, "error", err)
			}
		}
	}
}

func (r *Reconciler) lookupUserIDForEgress(ctx context.Context, username string) string {
	// Cheap: the users repo has GetByUsername. Empty when the user no
	// longer exists (just got deleted) — counter rows linger one tick
	// longer than the user, harmless.
	if r.users == nil {
		return ""
	}
	u, err := r.users.FindByUsername(ctx, username)
	if err != nil || u == nil {
		return ""
	}
	return u.ID
}
