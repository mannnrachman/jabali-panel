package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimiterConfig describes two token-bucket tiers. Default applies to
// most routes; Strict is used for credential-sensitive endpoints (/auth/*).
//
// If Strict{Rate,Burst} are zero, calls to Strict() fall back to the
// default bucket so the caller doesn't have to guard against unconfigured
// tiers.
type RateLimiterConfig struct {
	DefaultRate  rate.Limit
	DefaultBurst int
	StrictRate   rate.Limit
	StrictBurst  int
}

// RateLimiter holds per-IP, per-tier token buckets and returns middleware
// functions that consume from them. Buckets are created lazily and can be
// reaped by Cleanup (intended to run periodically from a background goroutine).
type RateLimiter struct {
	cfg RateLimiterConfig

	mu      sync.Mutex
	buckets map[bucketKey]*entry
}

type bucketKey struct {
	ip   string
	tier tier
}

type tier int

const (
	tierDefault tier = iota
	tierStrict
)

type entry struct {
	limiter *rate.Limiter
	seen    time.Time
}

// NewRateLimiter returns a ready limiter. Callers typically call this once
// at boot and reuse the same limiter across middleware registrations.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	return &RateLimiter{cfg: cfg, buckets: map[bucketKey]*entry{}}
}

// Default returns a middleware enforcing the default-tier bucket per client IP.
func (l *RateLimiter) Default() gin.HandlerFunc { return l.handler(tierDefault) }

// Strict returns a middleware enforcing the strict-tier bucket per client IP.
// Applied to expensive/sensitive POST endpoints (e.g. user re-provision) to
// bound replay or burst-retry patterns. Kratos owns its own credential-endpoint
// rate limiting; /.ory/* is not fronted by this middleware.
func (l *RateLimiter) Strict() gin.HandlerFunc { return l.handler(tierStrict) }

func (l *RateLimiter) handler(t tier) gin.HandlerFunc {
	return func(c *gin.Context) {
		lim := l.limiterFor(c.ClientIP(), t)
		res := lim.Reserve()
		if !res.OK() {
			// Can only happen with a misconfigured limit (rate=0 and burst=0);
			// still refuse deterministically.
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate_limited"})
			return
		}
		if d := res.Delay(); d > 0 {
			// We don't sleep — we reject, advising the client when to retry.
			res.Cancel() // refund the reservation we didn't use
			retry := int(math.Ceil(d.Seconds()))
			if retry < 1 {
				retry = 1
			}
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate_limited"})
			return
		}
		c.Next()
	}
}

func (l *RateLimiter) limiterFor(ip string, t tier) *rate.Limiter {
	key := bucketKey{ip: ip, tier: t}

	l.mu.Lock()
	defer l.mu.Unlock()

	if e, ok := l.buckets[key]; ok {
		e.seen = time.Now()
		return e.limiter
	}
	r, b := l.tierSettings(t)
	lim := rate.NewLimiter(r, b)
	l.buckets[key] = &entry{limiter: lim, seen: time.Now()}
	return lim
}

func (l *RateLimiter) tierSettings(t tier) (rate.Limit, int) {
	if t == tierStrict && l.cfg.StrictBurst > 0 {
		return l.cfg.StrictRate, l.cfg.StrictBurst
	}
	return l.cfg.DefaultRate, l.cfg.DefaultBurst
}

// Cleanup removes per-IP entries that haven't been hit in idleFor. Callers
// typically run this in a goroutine with a time.Ticker. Safe to call
// concurrently with request handling.
func (l *RateLimiter) Cleanup(idleFor time.Duration) {
	cutoff := time.Now().Add(-idleFor)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, e := range l.buckets {
		if e.seen.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
