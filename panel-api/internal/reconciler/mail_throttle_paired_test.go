package reconciler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/stalwartadmin"
)

// When both hourly + daily caps are set on one policy row, the
// reconciler must create TWO Stalwart MtaOutboundThrottle objects
// (one per window). v1 collapsed them into one (hourly wins, daily
// logged-only); v3 fixes that.
func TestReconcileMailThrottles_PairedCreatesTwoObjects(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal,
		MaxPerHour: 100, MaxPerDay: 5000, Enabled: true,
	}
	r.reconcileMailThrottles(context.Background())

	require.Len(t, cl.creates, 2, "expect one create per active rate window")
	assert.NotEmpty(t, repo.rows["row1"].StalwartID, "hourly id stamped")
	assert.NotEmpty(t, repo.rows["row1"].StalwartIDDaily, "daily id stamped")
}

func TestReconcileMailThrottles_OnlyHourlyWhenDailyZero(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal,
		MaxPerHour: 100, MaxPerDay: 0, Enabled: true,
	}
	r.reconcileMailThrottles(context.Background())
	assert.Len(t, cl.creates, 1)
	assert.NotEmpty(t, repo.rows["row1"].StalwartID)
	assert.Empty(t, repo.rows["row1"].StalwartIDDaily)
}

func TestReconcileMailThrottles_OnlyDailyWhenHourlyZero(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal,
		MaxPerHour: 0, MaxPerDay: 5000, Enabled: true,
	}
	r.reconcileMailThrottles(context.Background())
	assert.Len(t, cl.creates, 1)
	assert.Empty(t, repo.rows["row1"].StalwartID)
	assert.NotEmpty(t, repo.rows["row1"].StalwartIDDaily)
}

func TestReconcileMailThrottles_RemoveDailyWhenZeroedOut(t *testing.T) {
	r, repo, cl := throttleRecForTest(t)
	// Start with both populated upstream ids; row now has daily=0.
	repo.rows["row1"] = &models.MailOutboundPolicy{
		ID: "row1", Scope: models.OutboundScopeGlobal,
		MaxPerHour: 100, MaxPerDay: 0, Enabled: true,
		StalwartID: "stw-hourly-existing", StalwartIDDaily: "stw-daily-stale",
	}
	r.reconcileMailThrottles(context.Background())
	// Hourly updated, daily deleted.
	assert.Len(t, cl.updates, 1, "hourly update")
	require.Len(t, cl.deletes, 1, "daily delete")
	assert.Equal(t, "stw-daily-stale", cl.deletes[0])
	assert.Empty(t, repo.rows["row1"].StalwartIDDaily, "stale daily id cleared")
}

// Per-window payload shape: daily rate must have period=86400*1000 ms.
func TestThrottlePayloadForWindow_DailyRate(t *testing.T) {
	row := &models.MailOutboundPolicy{
		Scope: models.OutboundScopeGlobal, MaxPerDay: 5000, Enabled: true,
	}
	p := throttlePayloadForWindow(row, throttleWindowDaily)
	assert.Equal(t, uint64(5000), p.Rate.Count)
	assert.Equal(t, uint64(86400*1000), p.Rate.Period)
}

func TestThrottlePayloadForWindow_HourlyRate(t *testing.T) {
	row := &models.MailOutboundPolicy{
		Scope: models.OutboundScopeGlobal, MaxPerHour: 100, Enabled: true,
	}
	p := throttlePayloadForWindow(row, throttleWindowHourly)
	assert.Equal(t, uint64(100), p.Rate.Count)
	assert.Equal(t, uint64(3600*1000), p.Rate.Period)
	assert.Equal(t, stalwartadmin.HourlyRate(100), p.Rate)
}
