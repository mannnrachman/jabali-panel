package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// NotificationsDLQHandlerConfig wires the admin-facing DLQ inspector. The
// dispatcher moves any envelope past its retry budget to
// jabali:notifications:dlq with the original payload + a `reason` tag;
// this surface exposes XRANGE/XDEL/XADD against that stream so admins
// can replay or drop entries from the UI instead of SSHing in.
type NotificationsDLQHandlerConfig struct {
	Redis *redis.Client
	Log   *slog.Logger
}

// RegisterNotificationsDLQRoutes mounts:
//
//   - GET    /admin/notifications/dlq           list (paginated; default 50)
//   - POST   /admin/notifications/dlq/:id/replay  XADD back to main + XDEL
//   - DELETE /admin/notifications/dlq/:id       XDEL one entry
//   - DELETE /admin/notifications/dlq           XTRIM to zero (clear all)
//
// Routes are admin-gated. Nil Redis ⇒ register nothing (lab installs).
func RegisterNotificationsDLQRoutes(g *gin.RouterGroup, cfg NotificationsDLQHandlerConfig) {
	if cfg.Redis == nil {
		return
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	h := &notificationsDLQHandler{cfg: cfg}
	grp := g.Group("/admin/notifications/dlq", middleware.RequireAdmin())
	grp.GET("", h.list)
	grp.POST("/:id/replay", h.replay)
	grp.DELETE("/:id", h.drop)
	grp.DELETE("", h.clear)
}

type notificationsDLQHandler struct {
	cfg NotificationsDLQHandlerConfig
}

// dlqEntry is the wire shape returned by GET /admin/notifications/dlq.
// `Values` is the flat field map XADD'd by the dispatcher; the UI
// renders the relevant subset (event_kind, severity, title, body,
// reason, orig_id, channel_id) without us having to enumerate every
// possible key here.
type dlqEntry struct {
	ID     string            `json:"id"`
	At     string            `json:"at"`
	Values map[string]string `json:"values"`
}

type dlqListResponse struct {
	Data  []dlqEntry `json:"data"`
	Total int64      `json:"total"`
}

func (h *notificationsDLQHandler) list(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	// XLEN for total, XRevRangeN for the most recent N — ops want the
	// freshest failures at the top of the list, same shape as the
	// notification history page.
	total, err := h.cfg.Redis.XLen(ctx, notifications.StreamDLQ).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		h.cfg.Log.Error("dlq xlen failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "xlen failed"})
		return
	}
	msgs, err := h.cfg.Redis.XRevRangeN(ctx, notifications.StreamDLQ, "+", "-", int64(limit)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		h.cfg.Log.Error("dlq xrange failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "xrange failed"})
		return
	}
	out := make([]dlqEntry, 0, len(msgs))
	for _, m := range msgs {
		entry := dlqEntry{ID: m.ID, At: streamIDToTime(m.ID), Values: stringifyValues(m.Values)}
		out = append(out, entry)
	}
	c.JSON(http.StatusOK, dlqListResponse{Data: out, Total: total})
}

func (h *notificationsDLQHandler) replay(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	// Fetch the single entry by ID range [id, id].
	msgs, err := h.cfg.Redis.XRange(ctx, notifications.StreamDLQ, id, id).Result()
	if err != nil {
		h.cfg.Log.Error("dlq xrange single failed", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "xrange failed"})
		return
	}
	if len(msgs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	values := msgs[0].Values
	// Strip DLQ-specific fields before re-publishing — `reason` and
	// `orig_id` are bookkeeping that the dispatcher attaches in ToDLQ;
	// the main queue's consumer doesn't expect them and they'd just
	// ride along again on re-failure.
	cleaned := map[string]any{}
	for k, v := range values {
		if k == "reason" || k == "orig_id" {
			continue
		}
		cleaned[k] = v
	}
	pipe := h.cfg.Redis.TxPipeline()
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: notifications.StreamQueue, Values: cleaned})
	pipe.XDel(ctx, notifications.StreamDLQ, id)
	if _, err := pipe.Exec(ctx); err != nil {
		h.cfg.Log.Error("dlq replay pipeline failed", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "replay failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "replayed"})
}

func (h *notificationsDLQHandler) drop(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	deleted, err := h.cfg.Redis.XDel(ctx, notifications.StreamDLQ, id).Result()
	if err != nil {
		h.cfg.Log.Error("dlq xdel failed", "id", id, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "xdel failed"})
		return
	}
	if deleted == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *notificationsDLQHandler) clear(c *gin.Context) {
	ctx := c.Request.Context()
	// XTRIM with MAXLEN=0 truncates the stream — same effect as DEL but
	// keeps the consumer-group metadata so the dispatcher's reclaim
	// loop doesn't fault. (DEL would force EnsureGroup to recreate it.)
	if err := h.cfg.Redis.XTrimMaxLen(ctx, notifications.StreamDLQ, 0).Err(); err != nil {
		h.cfg.Log.Error("dlq xtrim failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "xtrim failed"})
		return
	}
	c.Status(http.StatusNoContent)
}

// streamIDToTime extracts the millisecond timestamp prefix of a Redis
// stream ID (`<ms>-<seq>`) and renders it as RFC3339. Returns "" on
// malformed input — UI falls back to the raw ID.
func streamIDToTime(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '-' {
			ms, err := strconv.ParseInt(id[:i], 10, 64)
			if err != nil {
				return ""
			}
			return time.UnixMilli(ms).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// stringifyValues collapses go-redis's map[string]any into map[string]string.
// XADD only ever stores strings; the any-typed return is a goredis
// idiom for forward-compat with future Redis types we don't use.
func stringifyValues(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case []byte:
			out[k] = string(t)
		default:
			// Fall through — fmt.Sprintf would also work but pulls in
			// fmt for one call. Repr unknown types as empty + the key
			// is still listed so the UI doesn't lose visibility.
			out[k] = ""
		}
	}
	return out
}
