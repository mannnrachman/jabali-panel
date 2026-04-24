package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
)

// perAdminBroadcastLimit is a tiny token-bucket-per-admin limiter for
// the M14 /broadcast + /channels/:id/test endpoints. The shared IP
// limiter in middleware/ratelimit already caps anonymous bursts; this
// adds anti-abuse on a shared admin terminal (two admins + the same
// jump host IP share a bucket otherwise).
//
// Bucket granularity: keyed by claims.UserID. Per-minute window so
// burst 5 maps directly to "5/min" in plan language. No distributed
// backing store — panel-api is single-process per host; HA pairs in
// Step 8.future would move this to Redis ZREMRANGEBYSCORE.
type perAdminBroadcastLimit struct {
	mu     sync.Mutex
	window time.Duration
	burst  int
	seen   map[string][]time.Time
	now    func() time.Time
}

func newBroadcastLimit(window time.Duration, burst int) *perAdminBroadcastLimit {
	return &perAdminBroadcastLimit{
		window: window,
		burst:  burst,
		seen:   map[string][]time.Time{},
		now:    time.Now,
	}
}

// allow advances the window for userID and returns true if the caller
// fits inside the burst budget. A false return should turn into a 429
// on the handler.
func (l *perAdminBroadcastLimit) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	hits := l.seen[userID]
	// Drop timestamps outside the window. Cheap because the slice is
	// small (burst + a handful) in steady state.
	fresh := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= l.burst {
		l.seen[userID] = fresh
		return false
	}
	fresh = append(fresh, now)
	l.seen[userID] = fresh
	return true
}

// middleware returns a gin handler that short-circuits with 429 when
// the caller has burned their burst budget. Uses the UserID on the
// auth claims set upstream by RequireKratosSession.
func (l *perAdminBroadcastLimit) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := ginctx.Claims(c)
		if claims == nil || claims.UserID == "" {
			// Unauthenticated paths should never reach this middleware
			// (RequireAdmin gates the route upstream). Fail closed.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		if !l.allow(claims.UserID) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limited",
				"message": "broadcast rate limit exceeded (5/min per admin)",
			})
			return
		}
		c.Next()
	}
}
