// Package api — server-status aggregator (M31, ADR-0065).
//
// One REST endpoint, GET /admin/server-status, returns the whole admin
// dashboard envelope in one shot. Backend fans out to the agent in
// parallel via errgroup with a hard cap on concurrent calls + a per-call
// timeout. A slow sub-call doesn't block the whole envelope — it gets
// flagged `timeout: true` in its slice and the rest still render.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

// AdminServerStatusHandlerConfig holds dependencies.
type AdminServerStatusHandlerConfig struct {
	Agent agent.AgentInterface
	// Redis is optional; when supplied the envelope includes the M14
	// notification dispatcher queue depths (main stream, DLQ, pending
	// consumer-group count). Nil ⇒ Queues is omitted.
	Redis *redis.Client
}

// RegisterAdminServerStatusRoutes mounts GET /admin/server-status.
// RequireAdmin gates the route. M31, ADR-0065.
func RegisterAdminServerStatusRoutes(g *gin.RouterGroup, cfg AdminServerStatusHandlerConfig) {
	if cfg.Agent == nil {
		return
	}
	h := &adminServerStatusHandler{cfg: cfg}
	grp := g.Group("/admin/server-status")
	grp.Use(middleware.RequireAdmin())
	grp.GET("", h.get)
}

type adminServerStatusHandler struct{ cfg AdminServerStatusHandlerConfig }

const (
	subCallTimeout = 5 * time.Second
	maxInFlight    = 8
)

// ServerStatusEnvelope is the shape returned to /admin/server-status. Sub-
// objects are pointers so a per-call failure can serialize as null
// rather than zero-valued (prevents the UI from rendering ghosts).
type ServerStatusEnvelope struct {
	AsOf       string              `json:"as_of"`
	Host       *json.RawMessage    `json:"host,omitempty"`
	CPU        *json.RawMessage    `json:"cpu,omitempty"`
	Network    *json.RawMessage    `json:"network,omitempty"`
	Services   *json.RawMessage    `json:"services,omitempty"`
	Processes  *json.RawMessage    `json:"processes,omitempty"`
	UserSlices *json.RawMessage    `json:"user_slices,omitempty"`
	Software   *json.RawMessage    `json:"software,omitempty"`
	Queues     *QueuesSlice        `json:"queues,omitempty"`
	Errors     map[string]string   `json:"errors,omitempty"`
	Alerts     []ServerStatusAlert `json:"alerts"`
}

// QueuesSlice is the M31.1 dispatcher-queue snapshot. All counters are
// best-effort; Redis hiccups surface as zeros + an entry in `errors`.
type QueuesSlice struct {
	NotificationsQueue   int64 `json:"notifications_queue"`
	NotificationsDLQ     int64 `json:"notifications_dlq"`
	NotificationsPending int64 `json:"notifications_pending"`
}

// ServerStatusAlert is one synthesized warning / critical row. Kind
// drives the UI icon; detail is human-readable. Step 4 will add link
// fields ("link": "/jabali-admin/updates") when applicable.
type ServerStatusAlert struct {
	Level  string `json:"level"`  // "warning" | "critical"
	Kind   string `json:"kind"`   // "disk" | "service" | "load" | ...
	Detail string `json:"detail"`
}

func (h *adminServerStatusHandler) get(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now().UTC()

	type slot struct {
		name string
		data json.RawMessage
		err  error
	}

	// errgroup runs the sub-calls in parallel and waits for all to
	// finish (or fail). We don't actually want errors to abort siblings
	// — every fan-out has its own deadline + we record the error per
	// slot rather than escalating, so the final envelope has every
	// available slice plus an `errors` map.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxInFlight)

	var (
		mu      sync.Mutex
		results = map[string]json.RawMessage{}
		errMap  = map[string]string{}
	)

	call := func(name, cmd string, params any) {
		g.Go(func() error {
			subCtx, cancel := context.WithTimeout(gctx, subCallTimeout)
			defer cancel()
			raw, err := h.cfg.Agent.Call(subCtx, cmd, params)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					errMap[name] = "timeout"
				} else {
					errMap[name] = err.Error()
				}
				return nil
			}
			results[name] = raw
			return nil
		})
	}

	call("host", "system.info", nil)
	call("cpu", "system.cpu_usage", nil)
	call("network", "system.network", nil)
	call("processes", "system.processes", nil)
	call("services", "system.service_details", nil)
	call("user_slices", "system.user_slices", nil)
	call("software", "system.software", nil)

	// M31.1 — Redis queue depths run in parallel with the agent calls.
	// Three XLEN/XPending pipelines, each with its own short timeout so
	// a Redis hiccup never delays the whole envelope.
	var queues *QueuesSlice
	if h.cfg.Redis != nil {
		g.Go(func() error {
			subCtx, cancel := context.WithTimeout(gctx, subCallTimeout)
			defer cancel()
			pipe := h.cfg.Redis.Pipeline()
			qLen := pipe.XLen(subCtx, notifications.StreamQueue)
			dlqLen := pipe.XLen(subCtx, notifications.StreamDLQ)
			pending := pipe.XPending(subCtx, notifications.StreamQueue, notifications.ConsumerGroup)
			if _, err := pipe.Exec(subCtx); err != nil && !errors.Is(err, redis.Nil) {
				mu.Lock()
				errMap["queues"] = err.Error()
				mu.Unlock()
				return nil
			}
			slice := &QueuesSlice{
				NotificationsQueue: qLen.Val(),
				NotificationsDLQ:   dlqLen.Val(),
			}
			if pending.Err() == nil && pending.Val() != nil {
				slice.NotificationsPending = pending.Val().Count
			}
			mu.Lock()
			queues = slice
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait()

	env := ServerStatusEnvelope{
		AsOf:   now.Format(time.RFC3339),
		Alerts: []ServerStatusAlert{},
	}
	if len(errMap) > 0 {
		env.Errors = errMap
	}
	if v, ok := results["host"]; ok {
		raw := v
		env.Host = &raw
	}
	if v, ok := results["cpu"]; ok {
		raw := v
		env.CPU = &raw
	}
	if v, ok := results["network"]; ok {
		raw := v
		env.Network = &raw
	}
	if v, ok := results["processes"]; ok {
		raw := v
		env.Processes = &raw
	}
	if v, ok := results["services"]; ok {
		raw := v
		env.Services = &raw
	}
	if v, ok := results["user_slices"]; ok {
		raw := v
		env.UserSlices = &raw
	}
	if v, ok := results["software"]; ok {
		raw := v
		env.Software = &raw
	}
	if queues != nil {
		env.Queues = queues
	}

	env.Alerts = synthesizeAlerts(results, errMap)
	if queues != nil {
		// Queue-depth alerts: a permanently-growing main stream means the
		// dispatcher is stuck; a DLQ above zero means at least one
		// envelope hit retry cap. Thresholds are intentionally low —
		// admins want to notice these immediately on the dashboard.
		if queues.NotificationsDLQ > 0 {
			noun := "entries"
			if queues.NotificationsDLQ == 1 {
				noun = "entry"
			}
			env.Alerts = append(env.Alerts, ServerStatusAlert{
				Level: "warning", Kind: "queue",
				Detail: "notification DLQ has " + formatInt64(queues.NotificationsDLQ) + " " + noun,
			})
		}
		if queues.NotificationsQueue > 1000 {
			env.Alerts = append(env.Alerts, ServerStatusAlert{
				Level: "critical", Kind: "queue",
				Detail: "notification queue backlog > 1000 (dispatcher stuck?)",
			})
		}
	}
	c.JSON(http.StatusOK, env)
}

// synthesizeAlerts turns raw sub-results into operator-visible alerts.
// Threshold rules are intentionally conservative — Step 1 ships a
// minimum that catches obvious failures; Step 4 will extend with
// service-specific rules and a link to the relevant remediation page.
func synthesizeAlerts(results map[string]json.RawMessage, errMap map[string]string) []ServerStatusAlert {
	var alerts []ServerStatusAlert

	for name, msg := range errMap {
		alerts = append(alerts, ServerStatusAlert{
			Level:  "warning",
			Kind:   "agent",
			Detail: "agent sub-call '" + name + "': " + msg,
		})
	}

	// Failed services → critical. Inactive services → critical only if
	// the unit is meant to be running on this host (UnitFileState ∈
	// {enabled, enabled-runtime, static, alias}). Lazy-started units
	// (e.g. jabali-webmail starts on the first domain.email_enable)
	// stay disabled until needed; flagging them inactive-critical
	// shows a permanent red banner on hosts with no mail domains.
	if rawSvc, ok := results["services"]; ok {
		var payload struct {
			Services []struct {
				Unit          string `json:"unit"`
				Active        string `json:"active"`
				LoadState     string `json:"load_state"`
				UnitFileState string `json:"unit_file_state"`
			} `json:"services"`
		}
		if err := json.Unmarshal(rawSvc, &payload); err == nil {
			for _, svc := range payload.Services {
				if svc.LoadState == "masked" {
					continue
				}
				if svc.Active == "failed" {
					alerts = append(alerts, ServerStatusAlert{
						Level: "critical", Kind: "service",
						Detail: svc.Unit + " is failed",
					})
					continue
				}
				if svc.Active == "inactive" && unitShouldBeRunning(svc.UnitFileState) {
					alerts = append(alerts, ServerStatusAlert{
						Level: "critical", Kind: "service",
						Detail: svc.Unit + " is inactive",
					})
				}
			}
		}
	}

	// Disk usage thresholds: > 80% warning, > 95% critical. Disk data
	// lives inside system.info → host.partitions.
	if rawHost, ok := results["host"]; ok {
		var payload struct {
			Partitions []struct {
				MountPoint string `json:"mount_point"`
				TotalBytes uint64 `json:"total_bytes"`
				UsedBytes  uint64 `json:"used_bytes"`
			} `json:"partitions"`
			LoadAvg  [3]float64 `json:"load_avg"`
			CPUCount int        `json:"cpu_count"`
		}
		if err := json.Unmarshal(rawHost, &payload); err == nil {
			for _, p := range payload.Partitions {
				if p.TotalBytes == 0 {
					continue
				}
				pct := float64(p.UsedBytes) * 100.0 / float64(p.TotalBytes)
				switch {
				case pct >= 95:
					alerts = append(alerts, ServerStatusAlert{
						Level: "critical", Kind: "disk",
						Detail: p.MountPoint + " is full (≥95%)",
					})
				case pct >= 80:
					alerts = append(alerts, ServerStatusAlert{
						Level: "warning", Kind: "disk",
						Detail: p.MountPoint + " over 80% used",
					})
				}
			}
			if payload.CPUCount > 0 && payload.LoadAvg[0] > float64(payload.CPUCount)*2 {
				alerts = append(alerts, ServerStatusAlert{
					Level: "warning", Kind: "load",
					Detail: "1m load avg exceeds 2× CPU count",
				})
			}
		}
	}

	return alerts
}

// formatInt64 keeps queue alert detail text dependency-free.
func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// unitShouldBeRunning returns true when a systemd unit's UnitFileState
// indicates the operator expects it to be active. "disabled" /
// "indirect" / "" all signal a unit that's intentionally idle (in this
// repo, the reconciler enables it lazily on first use, e.g.
// jabali-webmail on the first domain.email_enable).
func unitShouldBeRunning(unitFileState string) bool {
	switch unitFileState {
	case "enabled", "enabled-runtime", "static", "alias":
		return true
	}
	return false
}
