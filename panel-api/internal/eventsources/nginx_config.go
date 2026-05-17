package eventsources

import (
	"context"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
)

const (
	nginxConfigTick    = 2 * time.Minute
	nginxConfigCoolOff = 30 * time.Minute
)

// runNginxConfig polls the agent's nginx.test (nginx -t) every couple of
// minutes. A failing config is a whole-box outage: every reload is
// rejected and nginx serves a stale config (incident 2026-05-15 on mx —
// a build-window `http2 on;` on nginx<1.25.1). The agent owns the check
// (ADR-0050: panel-api holds no privileged shell). Genuine config
// failure → critical nginx.config.invalid; transport/agent-down errors
// are NOT this source's concern (service_down owns jabali-agent) and are
// filtered out so a flapping agent doesn't masquerade as bad config.
func runNginxConfig(ctx context.Context, d Deps) {
	if d.Agent == nil {
		if d.Log != nil {
			d.Log.Info("eventsources: nginx_config disabled (no agent)")
		}
		return
	}
	nginxConfigPass(ctx, d)
	tick := time.NewTicker(nginxConfigTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		nginxConfigPass(ctx, d)
	}
}

func nginxConfigPass(ctx context.Context, d Deps) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := d.Agent.Call(cctx, "nginx.test", nil)
	if err == nil {
		return // nginx -t OK
	}
	// nginxTestHandler returns AgentError "nginx test failed: <output>"
	// on a real config verdict. Anything else (timeout, agent down,
	// unknown method) is a different failure — don't cry "bad config".
	if !strings.Contains(err.Error(), "nginx test failed") {
		d.Log.Debug("eventsources: nginx_config skipped (non-verdict error)", "err", err)
		return
	}
	fireNginxConfigInvalid(ctx, d, err.Error())
}

func fireNginxConfigInvalid(ctx context.Context, d Deps, detail string) {
	if !shouldFire(ctx, d, "nginx.config.invalid", "nginx-t", nginxConfigCoolOff) {
		return
	}
	// Keep the body bounded — the nginx -t dump can be long.
	if len(detail) > 600 {
		detail = detail[:600] + "…"
	}
	_, err := d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "nginx.config.invalid",
		Severity:  models.NotificationSeverityCritical,
		Title:     "nginx config invalid — reloads rejected",
		Body: "`nginx -t` is failing on the host: every reload is rejected and " +
			"nginx is serving a stale config (wrong/expired cert, sites down). " +
			"Self-heal: `sudo jabali repair --auto` (nginx-config-invalid). " +
			"Detail: " + detail,
		Deeplink: "/admin/server-status",
	})
	if err != nil {
		d.Log.Warn("eventsources: publish nginx_config failed", "err", err)
	}
}
