// Package reconciler — daily bandwidth scan ticker (M13.1).
//
// Calls the agent's bandwidth.scan_day at 00:30 UTC daily, parses the
// per-domain {bytes, requests, day} stats, looks each domain name up in
// the panel DB, and upserts a row into bw_daily keyed by (domain_id,
// day). Non-existent domains in the agent response (e.g. logs left
// behind by a recently-deleted domain whose nginx site hasn't been
// pruned yet) are skipped silently.
package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// bwTickerLogger is the slog-shaped subset the ticker uses; matches
// SSL ticker's pattern so we don't fight a `*slog.Logger` direct
// dep import in tests.
type bwTickerLogger interface {
	Info(string, ...interface{})
	Warn(string, ...interface{})
	Error(string, ...interface{})
}

// StartBandwidthTicker runs the daily goaccess-driven bandwidth scan.
// Tick interval is 24h; we don't try to align to wall-clock 00:30 here
// because the ticker fires N hours after panel-api's last boot — close
// enough for a per-day metric whose precision is "yesterday".
//
// The ticker runs in its own goroutine and stops when ctx is cancelled.
func StartBandwidthTicker(ctx context.Context, ag agent.AgentInterface, domainRepo repository.DomainRepository, bwRepo repository.BWDailyRepository, log bwTickerLogger) {
	if ag == nil || domainRepo == nil || bwRepo == nil {
		return
	}
	const interval = 24 * time.Hour
	t := time.NewTicker(interval)
	defer t.Stop()
	log.Info("bw_ticker starting", "interval", interval.String())

	// Run once 60s after boot so an admin watching a fresh install sees
	// data populate without waiting a day. The agent's scan handler is
	// idempotent.
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
			scanBandwidth(ctx, ag, domainRepo, bwRepo, log)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Info("bw_ticker stopping")
			return
		case <-t.C:
			scanBandwidth(ctx, ag, domainRepo, bwRepo, log)
		}
	}
}

func scanBandwidth(ctx context.Context, ag agent.AgentInterface, domainRepo repository.DomainRepository, bwRepo repository.BWDailyRepository, log bwTickerLogger) {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	raw, err := ag.Call(callCtx, "bandwidth.scan_day", map[string]any{})
	if err != nil {
		log.Warn("bw_ticker: agent bandwidth.scan_day failed", "err", err)
		return
	}
	var resp struct {
		Stats []struct {
			Domain        string `json:"domain"`
			BytesTotal    uint64 `json:"bytes_total"`
			RequestsTotal uint64 `json:"requests_total"`
			Day           string `json:"day"` // YYYY-MM-DD
		} `json:"stats"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		log.Error("bw_ticker: parse agent response failed", "err", err)
		return
	}
	if len(resp.Skipped) > 0 {
		log.Info("bw_ticker: agent skipped some logs", "count", len(resp.Skipped), "first", resp.Skipped[0])
	}

	written := 0
	for _, s := range resp.Stats {
		day, err := time.Parse("2006-01-02", s.Day)
		if err != nil {
			log.Warn("bw_ticker: invalid day from agent", "domain", s.Domain, "day", s.Day, "err", err)
			continue
		}
		// Look up domain by name; skip if no DB row (orphan log).
		dom, err := domainRepo.FindByName(callCtx, s.Domain)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			log.Warn("bw_ticker: domain lookup failed", "domain", s.Domain, "err", err)
			continue
		}
		row := &models.BWDaily{
			DomainID:      dom.ID,
			Day:           day,
			BytesTotal:    s.BytesTotal,
			RequestsTotal: s.RequestsTotal,
		}
		if err := bwRepo.Upsert(callCtx, row); err != nil {
			log.Warn("bw_ticker: upsert failed", "domain", s.Domain, "err", err)
			continue
		}
		written++
	}
	log.Info("bw_ticker: scan complete", "domains", len(resp.Stats), "written", written, "skipped", len(resp.Skipped))
}
