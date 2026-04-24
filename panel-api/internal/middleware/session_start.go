package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// sessionSeenTTL bounds the dedupe window. Kratos cookies typically
// last 12h; anything past that we treat as a new session and fire a
// fresh admin.login notification.
const sessionSeenTTL = 12 * time.Hour

// TrackAdminLogin fires an admin.login notifications envelope the
// first time a given Kratos session cookie is seen against an admin
// user. Subsequent requests from the same session hit a Redis SETNX
// guard and skip the publish, so the inbox doesn't flood on polling
// SPAs.
//
// Requires: claims populated upstream (RequireKratosSession), Redis
// wired, publisher queue wired. Any missing dep downgrades to no-op —
// the middleware must never block the request itself.
func TrackAdminLogin(rdb *redis.Client, queue *notifications.Queue, log *slog.Logger) gin.HandlerFunc {
	if log == nil {
		log = slog.Default()
	}
	if rdb == nil || queue == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		defer c.Next()
		claims := ginctx.Claims(c)
		if claims == nil || claims.UserID == "" || !claims.IsAdmin {
			return
		}
		cookie, err := c.Cookie("ory_kratos_session")
		if err != nil || cookie == "" {
			return
		}
		// Hash the cookie before using it as a Redis key — avoids
		// persisting the raw session token in any logs or key dumps.
		digest := sha256.Sum256([]byte(cookie))
		key := "jabali:session-seen:" + hex.EncodeToString(digest[:16])

		// SETNX with TTL — first writer wins; everyone else skips.
		setCtx, cancel := context.WithTimeout(c.Request.Context(), 500*time.Millisecond)
		defer cancel()
		ok, err := rdb.SetNX(setCtx, key, "1", sessionSeenTTL).Result()
		if err != nil {
			log.Debug("admin-login tracker: redis setnx failed", "err", err)
			return
		}
		if !ok {
			return
		}

		// First-sight for this session. Fire envelope in a background
		// goroutine so the request doesn't pay Redis-stream latency on
		// the login landing request. 5s timeout is generous for an XADD.
		ip := c.ClientIP()
		ua := c.Request.UserAgent()
		email := claims.Email
		userID := claims.UserID
		logger := log
		go func() {
			pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer pubCancel()
			env := notifications.Envelope{
				EventKind: "admin.login",
				Severity:  models.NotificationSeverityInfo,
				Title:     fmt.Sprintf("Admin login: %s", email),
				Body:      fmt.Sprintf("IP %s  User-Agent: %s", ip, truncate(ua, 200)),
				UserID:    userID,
				Deeplink:  "/jabali-admin/notifications/history",
			}
			if _, err := queue.Publish(pubCtx, env); err != nil {
				logger.Warn("admin-login tracker: publish failed", "err", err)
			}
		}()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
