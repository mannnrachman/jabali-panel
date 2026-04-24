package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBroadcastLimit_Allow_BurstThenBlock(t *testing.T) {
	t.Parallel()
	l := newBroadcastLimit(time.Minute, 5)
	for i := 0; i < 5; i++ {
		require.True(t, l.allow("admin1"), "hit %d should be allowed", i)
	}
	require.False(t, l.allow("admin1"), "6th hit must be blocked")
}

func TestBroadcastLimit_PerUser(t *testing.T) {
	t.Parallel()
	l := newBroadcastLimit(time.Minute, 2)
	require.True(t, l.allow("a"))
	require.True(t, l.allow("a"))
	require.False(t, l.allow("a"))
	// different user has its own bucket
	require.True(t, l.allow("b"))
	require.True(t, l.allow("b"))
	require.False(t, l.allow("b"))
}

func TestBroadcastLimit_WindowRolls(t *testing.T) {
	t.Parallel()
	l := newBroadcastLimit(time.Minute, 2)
	// Inject a fake clock so we don't wait a real minute.
	fixed := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return fixed }
	require.True(t, l.allow("a"))
	require.True(t, l.allow("a"))
	require.False(t, l.allow("a"))
	// Advance past the window — fresh budget.
	fixed = fixed.Add(2 * time.Minute)
	require.True(t, l.allow("a"))
}

func TestBroadcastLimit_Middleware_429OnBurst(t *testing.T) {
	t.Parallel()
	repo := &fakeChannelsRepo{}
	q, _ := newQueueForChannelTest(t)
	r := newChannelsRouter(t, repo, q, newAdminCtx())

	// Fire 6 broadcasts under the same UserID; the 6th should be 429.
	var last int
	for i := 0; i < 6; i++ {
		rec := doNotifJSON(t, r, http.MethodPost, "/api/v1/admin/notifications/broadcast", map[string]any{
			"title": "m", "severity": "info",
		})
		last = rec.Code
		if i < 5 {
			require.Equal(t, http.StatusAccepted, rec.Code, "hit %d should succeed", i)
		}
	}
	require.Equal(t, http.StatusTooManyRequests, last)
}
