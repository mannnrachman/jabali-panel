// M47 Wave 6 ingest source — pulls DMARC RUA aggregate reports
// Stalwart has already parsed into its DmarcExternalReport schema
// objects and writes them into dmarc_aggregate. Notifies on each
// import so operators see a feed of "Google sent us a DMARC report
// for example.com (45 sources, 12% DKIM fail)".
//
// Cursor lives in the DB itself — the next poll requests
// `receivedAt:>{MostRecentWindowEnd}`. The repo's ExistsForReport
// gate catches re-deliveries beyond the cursor (Stalwart can
// re-import the same report after retry).
package eventsources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/notifications"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/stalwartadmin"
)

const (
	mailDmarcIngestTick    = 5 * time.Minute
	mailDmarcIngestTimeout = 60 * time.Second
	// stalwartCursorSlack — back the cursor off a bit so reports
	// arriving with slightly-stale receivedAt (Stalwart's clock vs
	// ours) don't get permanently skipped.
	stalwartCursorSlack = 1 * time.Hour
)

func runMailDmarcIngest(ctx context.Context, d Deps) {
	if d.StalwartAdmin == nil || d.DMARCAggregate == nil {
		d.Log.Debug("eventsources: mail_dmarc_ingest disabled (missing stalwart client or repo)")
		return
	}
	mailDmarcIngestPass(ctx, d)
	tick := time.NewTicker(mailDmarcIngestTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		mailDmarcIngestPass(ctx, d)
	}
}

func mailDmarcIngestPass(ctx context.Context, d Deps) {
	cctx, cancel := context.WithTimeout(ctx, mailDmarcIngestTimeout)
	defer cancel()
	cursor, err := d.DMARCAggregate.MostRecentWindowEnd(cctx)
	if err != nil {
		d.Log.Warn("dmarc-ingest: cursor read failed", "err", err)
		return
	}
	// Slack the cursor backwards by 1h so out-of-order receivedAt
	// values still get picked up.
	if !cursor.IsZero() {
		cursor = cursor.Add(-stalwartCursorSlack)
	}
	filter := ""
	if !cursor.IsZero() {
		filter = "receivedAt:>" + cursor.UTC().Format(time.RFC3339)
	}
	var raw json.RawMessage
	if filter != "" {
		raw, err = d.StalwartAdmin.Query(cctx, "DmarcExternalReport", filter)
	} else {
		raw, err = d.StalwartAdmin.Query(cctx, "DmarcExternalReport")
	}
	if err != nil {
		d.Log.Warn("dmarc-ingest: stalwart query failed", "err", err)
		return
	}
	var reports []stalwartadmin.DmarcExternalReport
	if err := json.Unmarshal(raw, &reports); err != nil {
		d.Log.Warn("dmarc-ingest: parse failed", "err", err)
		return
	}
	for _, rep := range reports {
		mailDmarcImportOne(cctx, d, rep)
	}
}

func mailDmarcImportOne(ctx context.Context, d Deps, rep stalwartadmin.DmarcExternalReport) {
	exists, err := d.DMARCAggregate.ExistsForReport(ctx, rep.Report.OrgName, rep.Report.DateRangeBegin, rep.Report.DateRangeEnd)
	if err == nil && exists {
		return
	}
	rows := make([]models.DMARCAggregate, 0, len(rep.Report.Records))
	for _, rec := range rep.Report.Records {
		rows = append(rows, models.DMARCAggregate{
			Domain:      rep.Report.Domain,
			Reporter:    defaultStr(rep.Report.OrgName, rep.From),
			WindowStart: rep.Report.DateRangeBegin,
			WindowEnd:   rep.Report.DateRangeEnd,
			SourceIP:    rec.SourceIP,
			Disposition: defaultStr(rec.Disposition, "none"),
			DKIM:        defaultStr(rec.DKIMResult, "fail"),
			SPF:         defaultStr(rec.SPFResult, "fail"),
			Cnt:         rec.Count,
		})
	}
	n, err := d.DMARCAggregate.InsertMany(ctx, rows)
	if err != nil {
		d.Log.Warn("dmarc-ingest: insert failed", "err", err, "reporter", rep.Report.OrgName)
		return
	}
	d.Log.Info("dmarc-ingest: imported", "reporter", rep.Report.OrgName, "domain", rep.Report.Domain, "rows", n)

	// One M14 dispatch per imported report. Body summarises the
	// failed-DKIM bucket count so the operator gets a quick gauge
	// without needing to open the detailed view.
	failed := uint(0)
	total := uint(0)
	for _, r := range rep.Report.Records {
		total += r.Count
		if !strings.EqualFold(r.DKIMResult, "pass") {
			failed += r.Count
		}
	}
	if d.Queue == nil {
		return
	}
	if !shouldFire(ctx, d, "mail.dmarc.report_received", rep.ID, 1*time.Minute) {
		return
	}
	body := fmt.Sprintf("DMARC report from %s for %s: %d sources, %d/%d DKIM-failing", rep.Report.OrgName, rep.Report.Domain, len(rep.Report.Records), failed, total)
	_, _ = d.Queue.Publish(ctx, notifications.Envelope{
		EventKind: "mail.dmarc.report_received",
		Severity:  pickDmarcSeverity(failed, total),
		Title:     "DMARC report received: " + rep.Report.Domain,
		Body:      body,
		Deeplink:  "/jabali-admin/mail/dmarc?domain=" + rep.Report.Domain,
	})
}

func pickDmarcSeverity(failed, total uint) string {
	if total == 0 {
		return "info"
	}
	// >10% DKIM-failing = warn; the operator should look.
	if failed*10 > total {
		return "warning"
	}
	return "info"
}

func defaultStr(s, dflt string) string {
	if strings.TrimSpace(s) == "" {
		return dflt
	}
	return s
}
