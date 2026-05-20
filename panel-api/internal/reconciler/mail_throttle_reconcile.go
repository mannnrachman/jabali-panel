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

func (r *Reconciler) reconcileMailThrottleOne(ctx context.Context, row *models.MailOutboundPolicy) {
	switch {
	case row.Enabled && row.StalwartID == "":
		r.throttleCreate(ctx, row)
	case row.Enabled && row.StalwartID != "":
		r.throttleUpdate(ctx, row)
	case !row.Enabled && row.StalwartID != "":
		r.throttleDelete(ctx, row)
	}
}

func (r *Reconciler) throttleCreate(ctx context.Context, row *models.MailOutboundPolicy) {
	cctx, cancel := context.WithTimeout(ctx, mailThrottleCallTimeout)
	defer cancel()
	payload := throttlePayloadFor(row)
	id, err := r.stalwartAdmin.Create(cctx, "MtaOutboundThrottle", payload)
	r.stampThrottleResult(ctx, row.ID, id, err)
}

func (r *Reconciler) throttleUpdate(ctx context.Context, row *models.MailOutboundPolicy) {
	cctx, cancel := context.WithTimeout(ctx, mailThrottleCallTimeout)
	defer cancel()
	payload := throttlePayloadFor(row)
	err := r.stalwartAdmin.Update(cctx, "MtaOutboundThrottle", row.StalwartID, payload)
	r.stampThrottleResult(ctx, row.ID, row.StalwartID, err)
}

func (r *Reconciler) throttleDelete(ctx context.Context, row *models.MailOutboundPolicy) {
	cctx, cancel := context.WithTimeout(ctx, mailThrottleCallTimeout)
	defer cancel()
	err := r.stalwartAdmin.Delete(cctx, "MtaOutboundThrottle", row.StalwartID)
	if err != nil {
		r.stampThrottleResult(ctx, row.ID, row.StalwartID, err)
		return
	}
	// Successful delete clears the upstream id so the next enable
	// re-creates from scratch.
	r.stampThrottleResult(ctx, row.ID, "", nil)
}

func (r *Reconciler) stampThrottleResult(ctx context.Context, rowID, stalwartID string, callErr error) {
	var lastErr *string
	if callErr != nil {
		msg := callErr.Error()
		lastErr = &msg
		r.log.Warn("mail-throttle: apply failed", "row", rowID, "err", callErr)
	} else {
		r.log.Info("mail-throttle: applied", "row", rowID, "stalwart_id", stalwartID)
	}
	if err := r.outboundPolicies.UpdateApplyState(ctx, rowID, stalwartID, lastErr); err != nil {
		r.log.Warn("mail-throttle: state stamp failed", "row", rowID, "err", err)
	}
}

// throttlePayloadFor maps one mail_outbound_policy row to the Stalwart
// wire shape. Stalwart's rate object accepts ONE window per object;
// hourly wins when both are set.
//
// Scope:
//   - global -> no key + always-fire match (server-wide cap)
//   - user   -> key=sender + match `sender == '<scope_ref>'`
//   - domain -> key=senderDomain + match `sender_domain == '<scope_ref>'`
//
// scope_ref MUST be sanitised by the API handler — the literal is
// embedded verbatim into Stalwart's Expression string and a stray
// quote would degrade the throttle into always-fire.
func throttlePayloadFor(row *models.MailOutboundPolicy) stalwartadmin.MtaOutboundThrottlePayload {
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
	rate := stalwartadmin.HourlyRate(uint64(row.MaxPerHour))
	if row.MaxPerHour == 0 && row.MaxPerDay > 0 {
		rate = stalwartadmin.DailyRate(uint64(row.MaxPerDay))
	}
	return stalwartadmin.MtaOutboundThrottlePayload{
		Description: throttleDescription(row),
		Enable:      row.Enabled,
		Key:         keyMap,
		Rate:        rate,
		Match:       match,
	}
}

func throttleDescription(row *models.MailOutboundPolicy) string {
	ref := "-"
	if row.ScopeRef != nil {
		ref = *row.ScopeRef
	}
	return fmt.Sprintf("jabali %s/%s h=%d d=%d", row.Scope, ref, row.MaxPerHour, row.MaxPerDay)
}
