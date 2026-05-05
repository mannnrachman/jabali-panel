package main

import (
	"context"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/kratosclient"
)

// waitForKratosReady polls Kratos's /admin/health/ready endpoint until it
// answers 2xx, up to `timeout`. Returns true if Kratos became ready,
// false on timeout.
//
// Purpose: close a boot-order race. install.sh runs install_kratos BEFORE
// build_backend + start_and_verify, so systemctl starts jabali-kratos
// seconds before jabali-panel. On a slow host (or one where Kratos restarts
// post-migration for any reason), jabali-panel can beat Kratos to binding
// the admin endpoint and crash its first BootstrapAdmin call with a raw
// "connection refused", triggering a systemd restart loop that never
// recovers because every restart loses to the same race.
//
// Takes a *kratosclient.Client rather than a raw URL so the poll uses the
// same transport everything else does — critical for M25 Step 2+, where
// the admin endpoint is a Unix socket that needs the client's custom
// DialContext to reach. Building a fresh http.Client here would silently
// fall back to TCP and timeout.
//
// A single poll loop before the first Kratos RPC — with a hard timeout
// so an operator who broke Kratos entirely still sees the panel come up
// (legacy fallback) rather than a panel in crash-loop forever.
func waitForKratosReady(client *kratosclient.Client, timeout time.Duration, log *slog.Logger) bool {
	if client == nil {
		return false
	}
	// Sub-second initial interval so healthy hosts don't pay a noticeable
	// startup penalty; exponential backoff to 2s so a broken Kratos doesn't
	// spin the CPU.
	interval := 200 * time.Millisecond
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := client.AdminReady(ctx)
		cancel()
		if err == nil {
			if attempt > 1 && log != nil {
				log.Info("Kratos became ready", "attempts", attempt)
			}
			return true
		}
		time.Sleep(interval)
		if interval < 2*time.Second {
			interval *= 2
		}
	}
	return false
}
