package backup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseCron_RejectsBadExpr(t *testing.T) {
	_, err := ParseCron("bad cron")
	require.Error(t, err)
}

func TestPresetCronExpr_ParsesCleanly(t *testing.T) {
	for name, expr := range PresetCronExpr {
		_, err := ParseCron(expr)
		require.NoError(t, err, "preset %s = %q must parse", name, expr)
	}
}

func TestNextFire_DailyAt3AM(t *testing.T) {
	from := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	next, err := NextFire(PresetCronExpr["daily"], from)
	require.NoError(t, err)
	require.Equal(t, 2026, next.Year())
	require.Equal(t, time.April, next.Month())
	require.Equal(t, 29, next.Day())
	require.Equal(t, 3, next.Hour())
}

func TestPreviewFires_ReturnsAscendingSequence(t *testing.T) {
	from := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	fires, err := PreviewFires(PresetCronExpr["weekly"], from, 3)
	require.NoError(t, err)
	require.Len(t, fires, 3)
	for i := 1; i < len(fires); i++ {
		require.True(t, fires[i].After(fires[i-1]), "fires[%d] must be after fires[%d]", i, i-1)
	}
}
