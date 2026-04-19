package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// waitForKratosReady polls Kratos's /admin/health/ready endpoint until it
// answers 2xx, up to `timeout`. Returns true if Kratos became ready,
// false on timeout.
//
// Purpose: close a boot-order race. install.sh runs install_kratos BEFORE
// build_backend + start_and_verify, so systemctl starts jabali-kratos
// seconds before jabali-panel. On a slow host (or one where Kratos restarts
// post-migration for any reason), jabali-panel can beat Kratos to binding
// the admin port and crash its first BootstrapAdmin call with a raw
// "connection refused", triggering a systemd restart loop that never
// recovers because every restart loses to the same race.
//
// A single poll loop before the first Kratos RPC — with a hard timeout
// so an operator who broke Kratos entirely still sees the panel come up
// (legacy fallback) rather than a panel in crash-loop forever.
func waitForKratosReady(adminURL string, timeout time.Duration, log *slog.Logger) bool {
	if adminURL == "" {
		return false
	}
	// Sub-second initial interval so healthy hosts don't pay a noticeable
	// startup penalty; exponential backoff to 2s so a broken Kratos doesn't
	// spin the CPU.
	interval := 200 * time.Millisecond
	deadline := time.Now().Add(timeout)
	attempt := 0
	url := adminURL + "/admin/health/ready"
	// Short per-request timeout — we care about liveness not latency;
	// if the TCP connect takes more than a second, it's not ready.
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		attempt++
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			return false // malformed URL — fail fast
		}
		resp, err := client.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if attempt > 1 && log != nil {
					log.Info("Kratos became ready", "attempts", attempt, "url", url)
				}
				return true
			}
		}
		time.Sleep(interval)
		if interval < 2*time.Second {
			interval *= 2
		}
	}
	return false
}
