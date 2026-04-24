package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// RegisterNotificationsInternalRoutes mounts the localhost-only enqueue
// endpoint agents (and future in-process event sources that want a REST
// surface) call to fan events into the M14 dispatcher. The route lives
// under /api/v1/internal/notifications and is gated by RequireLocalhost
// — unreachable from the outside world via nginx's upstream rules, but
// belt-and-braces-guarded anyway.
func RegisterNotificationsInternalRoutes(g *gin.RouterGroup, queue *notifications.Queue) {
	grp := g.Group("/internal/notifications")
	grp.Use(middleware.RequireLocalhost())
	grp.POST("/enqueue", func(c *gin.Context) {
		if queue == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "dispatcher not initialised (Redis missing)"})
			return
		}
		var req enqueueRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
			return
		}
		if err := req.validate(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		env := notifications.Envelope{
			EventKind:  req.EventKind,
			Severity:   req.Severity,
			Title:      req.Title,
			Body:       req.Body,
			Deeplink:   req.Deeplink,
			UserID:     req.UserID,
			ChannelIDs: req.ChannelIDs,
		}
		id, err := queue.Publish(c.Request.Context(), env)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "publish: " + err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"id": id})
	})
}

type enqueueRequest struct {
	EventKind  string   `json:"event_kind"`
	Severity   string   `json:"severity"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Deeplink   string   `json:"deeplink,omitempty"`
	UserID     string   `json:"user_id,omitempty"`
	ChannelIDs []string `json:"channel_ids,omitempty"`
}

// enqueueAllowedEventKinds mirrors the agent-side allowlist. Panel-api
// defense-in-depth check — the same validation runs here so that any
// future non-agent localhost caller (eventsources) benefits.
var enqueueAllowedEventKinds = map[string]struct{}{
	"cert.renew.ok":       {},
	"cert.renew.fail":     {},
	"disk.full.warn":      {},
	"disk.full.crit":      {},
	"service.down":        {},
	"crowdsec.ban.spike":  {},
	"backup.fail":         {},
	// Additional events that Step 2+ already emits from panel-api
	// itself land via direct Queue.Publish — not this handler — but
	// keep the list aligned with history.event_kind values so the
	// admin API's filter dropdown stays coherent.
	"domain.expiry.7d":             {},
	"domain.expiry.1d":             {},
	"notifications.channel.auto_disabled": {},
}

var enqueueAllowedSeverities = map[string]struct{}{
	"info":     {},
	"warning":  {},
	"error":    {},
	"critical": {},
}

func (r enqueueRequest) validate() error {
	if _, ok := enqueueAllowedEventKinds[r.EventKind]; !ok {
		return errBadField("event_kind not in allowlist")
	}
	if _, ok := enqueueAllowedSeverities[r.Severity]; !ok {
		return errBadField("severity not in allowlist")
	}
	if r.Title == "" {
		return errBadField("title required")
	}
	if len(r.Title) > 200 {
		return errBadField("title must be <= 200 chars")
	}
	if len(r.Body) > 2000 {
		return errBadField("body must be <= 2000 chars")
	}
	return nil
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func errBadField(msg string) error { return &validationError{msg: msg} }
