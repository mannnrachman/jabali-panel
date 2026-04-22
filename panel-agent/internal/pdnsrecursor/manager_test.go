package pdnsrecursor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- fakes ---

type fakeExec struct {
	mu        sync.Mutex
	calls     []string
	failAfter int32 // fail the Nth+1 call; -1 = never
	err       error
}

func (f *fakeExec) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	f.mu.Unlock()
	if int32(n) >= atomic.LoadInt32(&f.failAfter) && f.err != nil {
		return nil, f.err
	}
	return []byte("ok"), nil
}

type fakeProbe struct {
	mu      sync.Mutex
	calls   []string
	fail    bool
	failErr error
}

func (f *fakeProbe) ProbeZone(_ context.Context, zone string) error {
	f.mu.Lock()
	f.calls = append(f.calls, zone)
	f.mu.Unlock()
	if f.fail {
		if f.failErr != nil {
			return f.failErr
		}
		return errors.New("probe: injected failure")
	}
	return nil
}

// --- helpers ---

// newTestManager builds a Manager wired to $TMPDIR with stubbed exec+probe.
func newTestManager(t *testing.T) (*Manager, *fakeExec, *fakeProbe, string) {
	t.Helper()
	dir := t.TempDir()
	exec := &fakeExec{failAfter: 1 << 30} // effectively never
	probe := &fakeProbe{}
	m, err := New(Options{
		ForwardsPath: filepath.Join(dir, "recursor.forwards"),
		Exec:         exec,
		Prober:       probe,
		Owner:        "", // skip chown in tests (no pdns-recursor user in CI)
		Clock:        func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m, exec, probe, dir
}

func readLive(t *testing.T, m *Manager) string {
	t.Helper()
	data, err := os.ReadFile(m.opts.ForwardsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

// --- tests ---

func TestAddZone_FirstWrite(t *testing.T) {
	m, exec, probe, _ := newTestManager(t)
	ctx := context.Background()

	changed, err := m.AddZone(ctx, Entry{"example.com", "127.0.0.1", 5300})
	if err != nil {
		t.Fatalf("AddZone: %v", err)
	}
	if !changed {
		t.Error("first AddZone should report Changed=true")
	}
	if len(exec.calls) != 1 || !strings.Contains(exec.calls[0], "reload-zones") {
		t.Errorf("exec calls = %v, want one reload-zones", exec.calls)
	}
	if len(probe.calls) != 1 || probe.calls[0] != "example.com" {
		t.Errorf("probe calls = %v, want [example.com]", probe.calls)
	}
	body := readLive(t, m)
	if !strings.Contains(body, "example.com=127.0.0.1:5300") {
		t.Errorf("live file missing entry. body=%q", body)
	}
}

func TestAddZone_Idempotent(t *testing.T) {
	m, exec, probe, _ := newTestManager(t)
	ctx := context.Background()

	e := Entry{"example.com", "127.0.0.1", 5300}
	if _, err := m.AddZone(ctx, e); err != nil {
		t.Fatalf("first AddZone: %v", err)
	}
	execCallsAfterFirst := len(exec.calls)
	probeCallsAfterFirst := len(probe.calls)

	changed, err := m.AddZone(ctx, e)
	if err != nil {
		t.Fatalf("second AddZone: %v", err)
	}
	if changed {
		t.Error("second AddZone should report Changed=false")
	}
	if len(exec.calls) != execCallsAfterFirst {
		t.Errorf("second AddZone triggered exec calls %v — should have been silent", exec.calls[execCallsAfterFirst:])
	}
	if len(probe.calls) != probeCallsAfterFirst {
		t.Errorf("second AddZone triggered probe calls %v — should have been silent", probe.calls[probeCallsAfterFirst:])
	}
}

func TestAddZone_RollbackOnProbeFail(t *testing.T) {
	m, _, probe, _ := newTestManager(t)
	ctx := context.Background()

	// Seed good state: one zone already present.
	if _, err := m.AddZone(ctx, Entry{"existing.com", "127.0.0.1", 5300}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := readLive(t, m)

	// Now try to add a second zone; make the probe fail.
	probe.fail = true
	_, err := m.AddZone(ctx, Entry{"newzone.com", "127.0.0.1", 5300})
	if err == nil {
		t.Fatal("AddZone with failing probe should return error")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("error should mention probe: %v", err)
	}

	after := readLive(t, m)
	if after != before {
		t.Errorf("rollback failed.\nbefore=%q\nafter=%q", before, after)
	}
}

func TestAddZone_RollbackOnReloadFail(t *testing.T) {
	m, exec, _, _ := newTestManager(t)
	ctx := context.Background()

	// Seed first.
	if _, err := m.AddZone(ctx, Entry{"existing.com", "127.0.0.1", 5300}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := readLive(t, m)

	// Arm exec to fail the NEXT call (the reload after the next AddZone).
	exec.err = errors.New("rec_control: connection refused")
	atomic.StoreInt32(&exec.failAfter, int32(len(exec.calls))) // fail from this call on

	_, err := m.AddZone(ctx, Entry{"newzone.com", "127.0.0.1", 5300})
	if err == nil {
		t.Fatal("AddZone with failing reload should return error")
	}

	after := readLive(t, m)
	if after != before {
		t.Errorf("rollback failed.\nbefore=%q\nafter=%q", before, after)
	}
}

func TestRemoveZone(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	ctx := context.Background()

	// Add two, remove one, verify state.
	if _, err := m.AddZone(ctx, Entry{"a.com", "127.0.0.1", 5300}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := m.AddZone(ctx, Entry{"b.com", "127.0.0.1", 5300}); err != nil {
		t.Fatalf("add b: %v", err)
	}

	changed, err := m.RemoveZone(ctx, "a.com")
	if err != nil {
		t.Fatalf("RemoveZone a: %v", err)
	}
	if !changed {
		t.Error("RemoveZone should report Changed=true when zone existed")
	}

	body := readLive(t, m)
	if strings.Contains(body, "a.com=") {
		t.Errorf("a.com still present after remove: %q", body)
	}
	if !strings.Contains(body, "b.com=") {
		t.Errorf("b.com missing after unrelated remove: %q", body)
	}
}

func TestRemoveZone_Idempotent(t *testing.T) {
	m, exec, _, _ := newTestManager(t)
	ctx := context.Background()

	before := len(exec.calls)
	changed, err := m.RemoveZone(ctx, "nonexistent.com")
	if err != nil {
		t.Fatalf("RemoveZone of absent zone: %v", err)
	}
	if changed {
		t.Error("RemoveZone of absent zone should report Changed=false")
	}
	if len(exec.calls) != before {
		t.Errorf("RemoveZone of absent zone triggered exec calls %v", exec.calls[before:])
	}
}

func TestAddZone_ConcurrentDifferentZones(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	zones := []string{"a.com", "b.com", "c.com", "d.com", "e.com"}
	errs := make(chan error, len(zones))
	for _, z := range zones {
		wg.Add(1)
		go func(z string) {
			defer wg.Done()
			if _, err := m.AddZone(ctx, Entry{z, "127.0.0.1", 5300}); err != nil {
				errs <- err
			}
		}(z)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent AddZone err: %v", e)
	}

	body := readLive(t, m)
	for _, z := range zones {
		if !strings.Contains(body, z+"=127.0.0.1:5300") {
			t.Errorf("body missing %s. body=%q", z, body)
		}
	}
}

func TestList(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	ctx := context.Background()

	for _, z := range []string{"zzz.com", "aaa.com", "mmm.com"} {
		if _, err := m.AddZone(ctx, Entry{z, "127.0.0.1", 5300}); err != nil {
			t.Fatalf("add %s: %v", z, err)
		}
	}

	out, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, len(out))
	for i, e := range out {
		got[i] = e.Zone
	}
	want := []string{"aaa.com", "mmm.com", "zzz.com"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q want=%q (not sorted?)", i, got[i], want[i])
		}
	}
}

func TestAddZone_InvalidEntryRejectedBeforeWrite(t *testing.T) {
	m, exec, _, dir := newTestManager(t)
	ctx := context.Background()

	_, err := m.AddZone(ctx, Entry{"UPPERCASE.com", "127.0.0.1", 5300})
	if err == nil {
		t.Fatal("uppercase zone should be rejected")
	}
	// No file should have been created.
	if _, statErr := os.Stat(filepath.Join(dir, "recursor.forwards")); !os.IsNotExist(statErr) {
		t.Errorf("forwards file was created on invalid entry: %v", statErr)
	}
	if len(exec.calls) != 0 {
		t.Errorf("invalid AddZone triggered exec calls %v", exec.calls)
	}
}
