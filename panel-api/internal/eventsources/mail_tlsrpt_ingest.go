// M47 Wave 8 ingest source — TLS-RPT (RFC 8460) aggregate reports.
// Receivers report back when STARTTLS failed / their cert validation
// blew up — the canonical signal that MTA-STS or DANE just stopped
// working for one of the panel's domains.
package eventsources

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/stalwartadmin"
)

const (
	mailTlsRptIngestTick    = 5 * time.Minute
	mailTlsRptIngestTimeout = 60 * time.Second
)

func runMailTlsRptIngest(ctx context.Context, d Deps) {
	if d.StalwartAdmin == nil || d.TLSRPTAggregate == nil {
		d.Log.Debug("eventsources: mail_tlsrpt_ingest disabled (missing stalwart client or repo)")
		return
	}
	mailTlsRptIngestPass(ctx, d)
	tick := time.NewTicker(mailTlsRptIngestTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		mailTlsRptIngestPass(ctx, d)
	}
}

func mailTlsRptIngestPass(ctx context.Context, d Deps) {
	cctx, cancel := context.WithTimeout(ctx, mailTlsRptIngestTimeout)
	defer cancel()
	cursor, err := d.TLSRPTAggregate.MostRecentWindowEnd(cctx)
	if err != nil {
		d.Log.Warn("tlsrpt-ingest: cursor read failed", "err", err)
		return
	}
	if !cursor.IsZero() {
		cursor = cursor.Add(-stalwartCursorSlack)
	}
	filter := ""
	if !cursor.IsZero() {
		filter = "receivedAt:>" + cursor.UTC().Format(time.RFC3339)
	}
	var raw json.RawMessage
	if filter != "" {
		raw, err = d.StalwartAdmin.Query(cctx, "TlsExternalReport", filter)
	} else {
		raw, err = d.StalwartAdmin.Query(cctx, "TlsExternalReport")
	}
	if err != nil {
		d.Log.Warn("tlsrpt-ingest: stalwart query failed", "err", err)
		return
	}
	var reports []stalwartadmin.TlsExternalReport
	if err := json.Unmarshal(raw, &reports); err != nil {
		d.Log.Warn("tlsrpt-ingest: parse failed", "err", err)
		return
	}
	for _, rep := range reports {
		mailTlsRptImportOne(cctx, d, rep)
	}
}

func mailTlsRptImportOne(ctx context.Context, d Deps, rep stalwartadmin.TlsExternalReport) {
	for _, pol := range rep.Report.Policies {
		exists, err := d.TLSRPTAggregate.ExistsForReport(ctx, rep.Report.OrgName, rep.Report.DateRangeBegin, rep.Report.DateRangeEnd)
		if err == nil && exists {
			continue
		}
		rows := []models.TLSRPTAggregate{}
		// Roll up one row per failure result-type plus an overall
		// success row so the dashboard can render the success-rate
		// without re-parsing.
		if pol.TotalSuccessful > 0 {
			rows = append(rows, models.TLSRPTAggregate{
				Domain:       pol.Domain,
				Reporter:     defaultStr(rep.Report.OrgName, rep.From),
				WindowStart:  rep.Report.DateRangeBegin,
				WindowEnd:    rep.Report.DateRangeEnd,
				ResultType:   "successful-tls",
				SuccessCount: pol.TotalSuccessful,
				FailureCount: 0,
			})
		}
		for _, f := range pol.FailureDetails {
			rows = append(rows, models.TLSRPTAggregate{
				Domain:       pol.Domain,
				Reporter:     defaultStr(rep.Report.OrgName, rep.From),
				WindowStart:  rep.Report.DateRangeBegin,
				WindowEnd:    rep.Report.DateRangeEnd,
				ResultType:   f.ResultType,
				SuccessCount: 0,
				FailureCount: f.FailureCount,
			})
		}
		n, err := d.TLSRPTAggregate.InsertMany(ctx, rows)
		if err != nil {
			d.Log.Warn("tlsrpt-ingest: insert failed", "err", err, "domain", pol.Domain)
			continue
		}
		d.Log.Info("tlsrpt-ingest: imported", "domain", pol.Domain, "reporter", rep.Report.OrgName, "rows", n)

		if d.Queue == nil || pol.TotalFailure == 0 {
			continue
		}
		if !shouldFire(ctx, d, "mail.tls.report_received", rep.ID+pol.Domain, 1*time.Minute) {
			continue
		}
		body := fmt.Sprintf("TLS-RPT from %s for %s: %d sessions failed, %d succeeded", rep.Report.OrgName, pol.Domain, pol.TotalFailure, pol.TotalSuccessful)
		_, _ = d.Queue.Publish(ctx, notifications.Envelope{
			EventKind: "mail.tls.report_received",
			Severity:  "warning",
			Title:     "TLS-RPT received: " + pol.Domain,
			Body:      body,
			Deeplink:  "/jabali-admin/mail/tlsrpt?domain=" + pol.Domain,
		})
	}
}
