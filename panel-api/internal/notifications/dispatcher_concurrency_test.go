package notifications

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// gatedSender blocks on release before returning nil. Lets the test
// observe the number of in-flight sendOne goroutines at once.
type gatedSender struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
}

func (g *gatedSender) Kind() string { return "slack" }
func (g *gatedSender) Send(ctx context.Context, _ models.NotificationChannel, _ Envelope) error {
	cur := g.inFlight.Add(1)
	for {
		prev := g.peak.Load()
		if cur <= prev {
			break
		}
		if g.peak.CompareAndSwap(prev, cur) {
			break
		}
	}
	<-g.release
	g.inFlight.Add(-1)
	return nil
}

// TestDispatcher_ConcurrencyCap verifies MaxConcurrentSenders bounds
// the number of simultaneously running sendOne goroutines per envelope.
func TestDispatcher_ConcurrencyCap(t *testing.T) {
	t.Parallel()

	const cap = 3
	const targets = 10

	gs := &gatedSender{release: make(chan struct{})}
	rdb, _ := newRedis(t)
	queue := NewQueue(rdb)
	require.NoError(t, queue.EnsureGroup(context.Background()))

	reg := NewRegistry()
	reg.Register(gs)

	channels := &fakeChannels{byID: map[string]*models.NotificationChannel{}}
	for i := 0; i < targets; i++ {
		id := "ch" + string(rune('0'+i))
		channels.byID[id] = &models.NotificationChannel{ID: id, Kind: "slack", Name: id, Enabled: true}
		channels.enabled = append(channels.enabled, *channels.byID[id])
	}

	d, err := NewDispatcher(queue, reg, channels, &fakeHistory{}, &fakeWebhook{}, slog.New(slog.DiscardHandler), Config{
		BatchSize:            1,
		ReadBlock:            20 * time.Millisecond,
		ReclaimInterval:      50 * time.Millisecond,
		ReclaimMinIdle:       30 * time.Millisecond,
		MaxRetries:           3,
		CircuitBreakerLimit:  99,
		ShutdownGrace:        500 * time.Millisecond,
		MaxConcurrentSenders: cap,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = d.Start(ctx); close(done) }()

	_, err = queue.Publish(ctx, Envelope{
		EventKind: "disk.full.warn",
		Severity:  models.NotificationSeverityWarning,
		Title:     "t",
		Body:      "b",
	})
	require.NoError(t, err)

	// Wait until the peak settles at the cap. sendOne goroutines block
	// on release so `inFlight` climbs to cap then stalls.
	require.Eventually(t, func() bool { return gs.peak.Load() >= int32(cap) }, time.Second, 5*time.Millisecond)
	// Give the dispatcher extra scheduling slices; the peak must never
	// exceed the cap even with every target ready.
	time.Sleep(50 * time.Millisecond)
	require.LessOrEqual(t, gs.peak.Load(), int32(cap))

	// Release all in-flight sends so the fanout completes + dispatcher
	// can ACK + shutdown.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < targets; i++ {
			gs.release <- struct{}{}
		}
	}()
	wg.Wait()

	cancel()
	<-done
}
