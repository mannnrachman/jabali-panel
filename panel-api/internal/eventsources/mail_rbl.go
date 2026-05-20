package eventsources

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// M47 Wave 5 — RBL monitor. Periodically probes the server's
// outbound IP(s) against a curated free RBL set; transitions of the
// per-(ip, rbl) `Listed` flag drive M14 events:
//
//   - mail.rbl.listed   (a previously-clean IP just appeared on a list)
//   - mail.rbl.cleared  (a previously-listed IP came off; informational)
//
// Operators learn their IP is blacklisted before customers do (the
// #1 mail-deliverability support ticket). DNSBL queries are
// stateless A-record lookups against `<reversed-octets>.<rbl>` per
// RFC 5782; an A answer = listed (TXT carries the reason).
//
// Curated free baseline only. Paid lists with auth tokens (e.g.
// Spamhaus DQS at volume) are out of scope per ADR-0103.
const (
	mailRBLTick = 4 * time.Hour
	// mailRBLQueryTimeout per DNS lookup — generous so a single slow
	// RBL doesn't drag the whole pass, but bounded so a hung resolver
	// can't pin the goroutine forever.
	mailRBLQueryTimeout = 8 * time.Second
)

// curatedRBLs is the v1 free-baseline list (per ADR-0103 / plan §7).
// Order is stable so manifest / logs are predictable.
var curatedRBLs = []string{
	"zen.spamhaus.org",
	"bl.spamcop.net",
	"b.barracudacentral.org",
	"multi.surbl.org",
}

// Resolver is the narrow DNS slice the source needs. net.DefaultResolver
// satisfies it; tests inject a fake to avoid live DNS during CI.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// mailRBLResolver overridable for tests. Defaults to the system resolver.
var mailRBLResolver Resolver = net.DefaultResolver

func runMailRBL(ctx context.Context, d Deps) {
	if d.MailRBLStates == nil || d.ServerSettings == nil {
		d.Log.Debug("eventsources: mail_rbl disabled (missing repo or settings)")
		return
	}
	mailRBLPass(ctx, d)
	tick := time.NewTicker(mailRBLTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		mailRBLPass(ctx, d)
	}
}

func mailRBLPass(ctx context.Context, d Deps) {
	st, err := d.ServerSettings.Get(ctx)
	if err != nil || st == nil || st.PublicIPv4 == "" {
		d.Log.Debug("eventsources: mail_rbl skip (no public_ipv4 in server_settings)")
		return
	}
	ip := strings.TrimSpace(st.PublicIPv4)
	if !isIPv4(ip) {
		// IPv6 deferred — RFC 5782 nibble-encoding + reach varies per
		// list; v1 ships v4 only (per plan).
		return
	}
	for _, rbl := range curatedRBLs {
		probeRBL(ctx, d, ip, rbl)
	}
}

// probeRBL does one (ip, rbl) lookup, upserts state, and emits an M14
// event ONLY when Listed transitions vs the prior stored row.
func probeRBL(ctx context.Context, d Deps, ip, rbl string) {
	host := reverseDNSBLQuery(ip, rbl)
	if host == "" {
		return
	}
	listed, detail := dnsblProbe(ctx, host)

	prev, gerr := d.MailRBLStates.GetByIPRBL(ctx, ip, rbl)
	wasListed := false
	if gerr == nil && prev != nil {
		wasListed = prev.Listed
	} else if gerr != nil && !errors.Is(gerr, repository.ErrNotFound) {
		d.Log.Warn("eventsources: mail_rbl previous lookup failed",
			"ip", ip, "rbl", rbl, "err", gerr)
	}

	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	if _, uerr := d.MailRBLStates.Upsert(ctx, ip, rbl, listed, detailPtr, d.Now()); uerr != nil {
		d.Log.Warn("eventsources: mail_rbl upsert failed",
			"ip", ip, "rbl", rbl, "err", uerr)
		return
	}

	// Transition-only dispatch. Steady state (every 4h probe with no
	// change) writes the row + checked_at and dispatches NOTHING.
	dedupeTag := fmt.Sprintf("rbl=%s ip=%s", rbl, ip)
	switch {
	case !wasListed && listed:
		d.Log.Warn("eventsources: mail_rbl IP newly listed",
			"ip", ip, "rbl", rbl, "detail", detail)
		if !shouldFire(ctx, d, "mail.rbl.listed", dedupeTag, mailRBLTick) {
			return
		}
		_, err := d.Queue.Publish(ctx, notifications.Envelope{
			EventKind: "mail.rbl.listed",
			Severity:  "critical",
			Title:     fmt.Sprintf("Mail IP %s listed on %s", ip, rbl),
			Body:      fmt.Sprintf("The server's outbound mail IP %s is now listed on the RBL %s. Outbound deliverability may be affected. RBL detail: %s. (dedupe: %s)", ip, rbl, detail, dedupeTag),
			Deeplink:  "/jabali-admin/server-status",
		})
		if err != nil {
			d.Log.Warn("eventsources: publish mail.rbl.listed failed",
				"ip", ip, "rbl", rbl, "err", err)
		}
	case wasListed && !listed:
		if !shouldFire(ctx, d, "mail.rbl.cleared", dedupeTag+" cleared", mailRBLTick) {
			return
		}
		_, err := d.Queue.Publish(ctx, notifications.Envelope{
			EventKind: "mail.rbl.cleared",
			Severity:  "info",
			Title:     fmt.Sprintf("Mail IP %s cleared from %s", ip, rbl),
			Body:      fmt.Sprintf("The server's outbound mail IP %s is no longer listed on the RBL %s. Outbound deliverability restored on that RBL. (dedupe: %s cleared)", ip, rbl, dedupeTag),
			Deeplink:  "/jabali-admin/server-status",
		})
		if err != nil {
			d.Log.Warn("eventsources: publish mail.rbl.cleared failed",
				"ip", ip, "rbl", rbl, "err", err)
		}
	}
}

// reverseDNSBLQuery builds the lookup name per RFC 5782:
// 1.2.3.4 → 4.3.2.1.<rbl>. Returns "" for non-IPv4 input.
func reverseDNSBLQuery(ipv4, rbl string) string {
	parts := strings.Split(ipv4, ".")
	if len(parts) != 4 {
		return ""
	}
	return parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0] + "." + rbl
}

// dnsblProbe does the A-then-TXT lookup. listed=true when A resolves
// to anything; detail is the first TXT record if any (best-effort —
// some RBLs return only an A code).
func dnsblProbe(parentCtx context.Context, host string) (listed bool, detail string) {
	ctx, cancel := context.WithTimeout(parentCtx, mailRBLQueryTimeout)
	defer cancel()
	addrs, err := mailRBLResolver.LookupHost(ctx, host)
	if err != nil {
		// NXDOMAIN / no-such-host = not listed; that's the success
		// path. Other errors (timeout, SERVFAIL) we treat as "not
		// listed" too rather than risk false alarms — the periodic
		// re-probe corrects on the next pass. A separate counter for
		// query-error rate is a follow-up.
		return false, ""
	}
	if len(addrs) == 0 {
		return false, ""
	}
	// TXT detail: best-effort, separate timeout window.
	txtCtx, txtCancel := context.WithTimeout(parentCtx, mailRBLQueryTimeout)
	defer txtCancel()
	if txts, terr := mailRBLResolver.LookupTXT(txtCtx, host); terr == nil && len(txts) > 0 {
		return true, txts[0]
	}
	return true, ""
}

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}
