package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// SSOPhpMyAdminRateLimiter implements a per-user token-bucket rate limiter
// for SSO operations. Limit: 10 issues per user per minute.
//
// TODO: replace with Redis/shared-store when multi-node.
type SSOPhpMyAdminRateLimiter struct {
	limit      rate.Limit
	burst      int
	mu         sync.Mutex
	users      map[string]*rate.Limiter
	lastSeen   map[string]time.Time
}

// NewSSOPhpMyAdminRateLimiter creates a new per-user rate limiter
// with a limit of 10 issues per minute per user.
func NewSSOPhpMyAdminRateLimiter() *SSOPhpMyAdminRateLimiter {
	return &SSOPhpMyAdminRateLimiter{
		limit:      rate.Every(6 * time.Second), // 10 per minute = 1 every 6 seconds
		burst:      1,
		users:      make(map[string]*rate.Limiter),
		lastSeen:   make(map[string]time.Time),
	}
}

// Allow checks if the user can issue an SSO token. Returns (allowed, retryAfter).
func (s *SSOPhpMyAdminRateLimiter) Allow(userID string) (bool, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	limiter, exists := s.users[userID]
	if !exists {
		limiter = rate.NewLimiter(s.limit, s.burst)
		s.users[userID] = limiter
	}

	s.lastSeen[userID] = time.Now()

	if limiter.Allow() {
		return true, 0
	}

	// Calculate retry-after based on next available token time
	reservation := limiter.Reserve()
	if !reservation.OK() {
		return false, 60 // fallback: 60 seconds
	}
	delay := reservation.Delay()
	reservation.Cancel()

	return false, delay.Seconds()
}

// Middleware returns a Gin middleware that enforces the rate limit.
// If limit is exceeded, responds with 429 and a JSON error.
func (s *SSOPhpMyAdminRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract user ID from context (assumes JWT middleware has set it)
		userID, exists := c.Get("user_id")
		if !exists {
			// No user context; deny for safety
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "rate_limited",
				"message": "rate limit check failed: no user context",
			})
			c.Abort()
			return
		}

		userIDStr, ok := userID.(string)
		if !ok || userIDStr == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "rate_limited",
				"message": "rate limit check failed: invalid user context",
			})
			c.Abort()
			return
		}

		allowed, retryAfter := s.Allow(userIDStr)
		if !allowed {
			c.Header("Retry-After", fmt.Sprintf("%.0f", retryAfter))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":                "rate_limited",
				"retry_after_seconds":  retryAfter,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Cleanup removes stale per-user buckets older than maxAge.
// Call this periodically from a background goroutine.
func (s *SSOPhpMyAdminRateLimiter) Cleanup(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for userID, lastSeen := range s.lastSeen {
		if now.Sub(lastSeen) > maxAge {
			delete(s.users, userID)
			delete(s.lastSeen, userID)
		}
	}
}
