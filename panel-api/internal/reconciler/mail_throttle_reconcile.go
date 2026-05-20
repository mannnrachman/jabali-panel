// Package reconciler — M47 Wave 3 outbound-throttle convergence.
//
// Reads mail_outbound_policy on each tick and ensures Stalwart's
// MtaOutboundThrottle object matches. State machine:
//
//	row.enabled  && stalwart_id=='' → Create on Stalwart, persist id
//	row.enabled  && stalwart_id!='' → Update on Stalwart (idempotent)
//	!row.enabled && stalwart_id!='' → Delete on Stalwart, clear id
//	!row.enabled && stalwart_id=='' → no-op
//
// Failures stamp last_error on the row + leave stalwart_id where it
// was, so the next tick retries. Self-healing.
package reconciler

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/stalwartadmin"
)

// ThrottleStalwartClient is the narrow CRUD slice Wave 3 needs.
// Subset of *stalwartadmin.Client; tests inject a fake.
type ThrottleStalwartClient interface {
	Create(ctx context.Context, typeName string, payload any) (string, error)
	Update(ctx context.Context, typeName, id string, payload any) error
	Delete(ctx context.Context, typeName, id string) error
}

const mailThrottleCallTimeout = 30 * time.Second

// reconcileMailThrottles converges every mail_outbound_policy row.
// Called from the main reconcile loop on each tick. Self-disables
// when the policy repo or Stalwart client isn't wired.
func (r *Reconciler) reconcileMailThrottles(ctx context.Context) {
	if r.outboundPolicies == nil || r.stalwartAdmin == nil {
		return
	}
	rows, err := r.outboundPolicies.List(ctx)
	if err != nil {
		r.log.Warn("mail-throttle: list failed", "err", err)
		return
	}
	for i := range rows {
		r.reconcileMailThrottleOne(ctx, &rows[i])
	}
}

// reconcileMailThrottleOne converges BOTH rate windows independently.
// One mail_outbound_policy row can produce up to two Stalwart
// MtaOutboundThrottle objects: one for the hourly cap (StalwartID),
// one for the daily cap (StalwartIDDaily). Either, both, or neither
// can be active depending on max_per_hour / max_per_day. The
// reconciler treats each window as its own state machine.
func (r *Reconciler) reconcileMailThrottleOne(ctx context.Context, row *models.MailOutboundPolicy) {
	r.reconcileThrottleWindow(ctx, row, throttleWindowHourly)
	r.reconcileThrottleWindow(ctx, row, throttleWindowDaily)
}

type throttleWindow int

const (
	throttleWindowHourly throttleWindow = iota
	throttleWindowDaily
)

func (r *Reconciler) reconcileThrottleWindow(ctx context.Context, row *models.MailOutboundPolicy, w throttleWindow) {
	var count uint
	var currentID string
	switch w {
	case throttleWindowHourly:
		count = row.MaxPerHour
		currentID = row.StalwartID
	case throttleWindowDaily:
		count = row.MaxPerDay
		currentID = row.StalwartIDDaily
	}
	wantActive := row.Enabled && count > 0

	switch {
	case wantActive && currentID == "":
		r.throttleWindowCreate(ctx, row, w)
	case wantActive && currentID != "":
		r.throttleWindowUpdate(ctx, row, w, currentID)
	case !wantActive && currentID != "":
		r.throttleWindowDelete(ctx, row, w, currentID)
	}
}

func (r *Reconciler) throttleWindowCreate(ctx context.Context, row *models.MailOutboundPolicy, w throttleWindow) {
	cctx, cancel := context.WithTimeout(ctx, mailThrottleCallTimeout)
	defer cancel()
	payload := throttlePayloadForWindow(row, w)
	id, err := r.stalwartAdmin.Create(cctx, "MtaOutboundThrottle", payload)
	r.stampThrottleWindowResult(ctx, row.ID, w, id, err)
}

func (r *Reconciler) throttleWindowUpdate(ctx context.Context, row *models.MailOutboundPolicy, w throttleWindow, currentID string) {
	cctx, cancel := context.WithTimeout(ctx, mailThrottleCallTimeout)
	defer cancel()
	payload := throttlePayloadForWindow(row, w)
	err := r.stalwartAdmin.Update(cctx, "MtaOutboundThrottle", currentID, payload)
	r.stampThrottleWindowResult(ctx, row.ID, w, currentID, err)
}

func (r *Reconciler) throttleWindowDelete(ctx context.Context, row *models.MailOutboundPolicy, w throttleWindow, currentID string) {
	cctx, cancel := context.WithTimeout(ctx, mailThrottleCallTimeout)
	defer cancel()
	err := r.stalwartAdmin.Delete(cctx, "MtaOutboundThrottle", currentID)
	if err != nil {
		r.stampThrottleWindowResult(ctx, row.ID, w, currentID, err)
		return
	}
	r.stampThrottleWindowResult(ctx, row.ID, w, "", nil)
}

func (r *Reconciler) stampThrottleWindowResult(ctx context.Context, rowID string, w throttleWindow, stalwartID string, callErr error) {
	var lastErr *string
	if callErr != nil {
		msg := callErr.Error()
		lastErr = &msg
		r.log.Warn("mail-throttle: apply failed", "row", rowID, "window", throttleWindowName(w), "err", callErr)
	} else {
		r.log.Info("mail-throttle: applied", "row", rowID, "window", throttleWindowName(w), "stalwart_id", stalwartID)
	}
	var err error
	if w == throttleWindowHourly {
		err = r.outboundPolicies.UpdateApplyState(ctx, rowID, stalwartID, lastErr)
	} else {
		err = r.outboundPolicies.UpdateApplyStateDaily(ctx, rowID, stalwartID, lastErr)
	}
	if err != nil {
		r.log.Warn("mail-throttle: state stamp failed", "row", rowID, "window", throttleWindowName(w), "err", err)
	}
}

func throttleWindowName(w throttleWindow) string {
	if w == throttleWindowDaily {
		return "daily"
	}
	return "hourly"
}

// throttlePayloadForWindow renders one Stalwart MtaOutboundThrottle
// payload for either the hourly or daily window of a policy row.
// Scope keying + Expression match are identical across windows; only
// rate differs.
//
// scope_ref MUST be sanitised by the API handler — the literal is
// embedded verbatim into Stalwart's Expression string and a stray
// quote would degrade the throttle into always-fire.
func throttlePayloadForWindow(row *models.MailOutboundPolicy, w throttleWindow) stalwartadmin.MtaOutboundThrottlePayload {
	keyMap := map[string]bool{}
	match := stalwartadmin.NewAlwaysFireMatch()
	switch row.Scope {
	case models.OutboundScopeUser:
		keyMap[stalwartadmin.ThrottleKeySender] = true
		if row.ScopeRef != nil && *row.ScopeRef != "" {
			match = stalwartadmin.NewSenderFilterMatch(*row.ScopeRef)
		}
	case models.OutboundScopeDomain:
		keyMap[stalwartadmin.ThrottleKeySenderDomain] = true
		if row.ScopeRef != nil && *row.ScopeRef != "" {
			match = stalwartadmin.NewSenderDomainFilterMatch(*row.ScopeRef)
		}
	}
	var rate stalwartadmin.MtaThrottleRate
	if w == throttleWindowDaily {
		rate = stalwartadmin.DailyRate(uint64(row.MaxPerDay))
	} else {
		rate = stalwartadmin.HourlyRate(uint64(row.MaxPerHour))
	}
	return stalwartadmin.MtaOutboundThrottlePayload{
		Description: throttleDescription(row, w),
		Enable:      row.Enabled,
		Key:         keyMap,
		Rate:        rate,
		Match:       match,
	}
}

func throttleDescription(row *models.MailOutboundPolicy, w throttleWindow) string {
	ref := "-"
	if row.ScopeRef != nil {
		ref = *row.ScopeRef
	}
	return fmt.Sprintf("jabali %s/%s/%s h=%d d=%d", row.Scope, ref, throttleWindowName(w), row.MaxPerHour, row.MaxPerDay)
}
