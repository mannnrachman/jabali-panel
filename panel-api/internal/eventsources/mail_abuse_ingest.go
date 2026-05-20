// M47 Wave 4 ingest source — ARF (RFC 5965) abuse feedback reports.
// One row per inbound feedback envelope ("user marked your message as
// spam" from Gmail/Microsoft/Yahoo postmaster); rate of incoming
// reports is the deliverability signal operators care about.
//
// Dispatches `mail.feedback.received` on every import; the dashboard
// (Wave 9) aggregates the rate per source-domain.
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
	mailAbuseIngestTick    = 5 * time.Minute
	mailAbuseIngestTimeout = 60 * time.Second
)

func runMailAbuseIngest(ctx context.Context, d Deps) {
	if d.StalwartAdmin == nil || d.ARFReports == nil {
		d.Log.Debug("eventsources: mail_abuse_ingest disabled (missing stalwart client or repo)")
		return
	}
	mailAbuseIngestPass(ctx, d)
	tick := time.NewTicker(mailAbuseIngestTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		mailAbuseIngestPass(ctx, d)
	}
}

func mailAbuseIngestPass(ctx context.Context, d Deps) {
	cctx, cancel := context.WithTimeout(ctx, mailAbuseIngestTimeout)
	defer cancel()
	cursor, err := d.ARFReports.MostRecentReceivedAt(cctx)
	if err != nil {
		d.Log.Warn("abuse-ingest: cursor read failed", "err", err)
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
		raw, err = d.StalwartAdmin.Query(cctx, "ArfExternalReport", filter)
	} else {
		raw, err = d.StalwartAdmin.Query(cctx, "ArfExternalReport")
	}
	if err != nil {
		d.Log.Warn("abuse-ingest: stalwart query failed", "err", err)
		return
	}
	var reports []stalwartadmin.ArfExternalReport
	if err := json.Unmarshal(raw, &reports); err != nil {
		d.Log.Warn("abuse-ingest: parse failed", "err", err)
		return
	}
	if len(reports) == 0 {
		return
	}
	rows := make([]models.ARFReport, 0, len(reports))
	for _, rep := range reports {
		exists, _ := d.ARFReports.ExistsForStalwartID(cctx, rep.ID)
		if exists {
			continue
		}
		arrival := rep.Report.ArrivalDate
		var arrivalPtr *time.Time
		if !arrival.IsZero() {
			arrivalPtr = &arrival
		}
		rows = append(rows, models.ARFReport{
			StalwartID:       rep.ID,
			ReceivedAt:       rep.ReceivedAt,
			FeedbackType:     defaultStr(rep.Report.FeedbackType, "abuse"),
			Reporter:         rep.From,
			OriginalRcpt:     rep.Report.OriginalRcptTo,
			OriginalMailFrom: rep.Report.OriginalMailFrom,
			SourceIP:         rep.Report.SourceIP,
			Incidents:        clampUint(rep.Report.IncidentsCount, 1),
			UserAgent:        rep.Report.UserAgent,
			ReportingMTA:     rep.Report.ReportingMTA,
			ArrivalDate:      arrivalPtr,
		})
	}
	n, err := d.ARFReports.InsertMany(cctx, rows)
	if err != nil {
		d.Log.Warn("abuse-ingest: insert failed", "err", err)
		return
	}
	d.Log.Info("abuse-ingest: imported", "rows", n)
	if d.Queue == nil || n == 0 {
		return
	}
	if !shouldFire(cctx, d, "mail.feedback.received", time.Now().UTC().Format(time.RFC3339), 5*time.Minute) {
		return
	}
	body := fmt.Sprintf("%d new abuse-feedback report(s) imported from upstream postmasters", n)
	_, _ = d.Queue.Publish(cctx, notifications.Envelope{
		EventKind: "mail.feedback.received",
		Severity:  "warning",
		Title:     "ARF feedback reports received",
		Body:      body,
		Deeplink:  "/jabali-admin/mail/feedback",
	})
}

func clampUint(v, min uint) uint {
	if v < min {
		return min
	}
	return v
}
