package commands

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdnsrecursor"
)

// --- fakes reused across handler tests ---

type testExec struct {
	calls []string
	err   error
}

func (t *testExec) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	t.calls = append(t.calls, name+" "+strings.Join(args, " "))
	return []byte("ok"), t.err
}

type testProbe struct {
	fail  bool
	calls []string
}

func (t *testProbe) ProbeZone(_ context.Context, zone string) error {
	t.calls = append(t.calls, zone)
	if t.fail {
		return errors.New("probe fail")
	}
	return nil
}

// newTestCmdManager builds a Manager pointed at $TMPDIR and installs it as
// the commands-package singleton. Returns a cleanup callback to reset the
// singleton for the next test.
func newTestCmdManager(t *testing.T) (*pdnsrecursor.Manager, *testExec, *testProbe, func()) {
	t.Helper()
	dir := t.TempDir()
	exec := &testExec{}
	probe := &testProbe{}
	m, err := pdnsrecursor.New(pdnsrecursor.Options{
		ForwardsPath: filepath.Join(dir, "recursor.forwards"),
		Exec:         exec,
		Prober:       probe,
		SkipChown:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	setRecursorMgrForTest(m)
	return m, exec, probe, resetRecursorMgrForTest
}

// --- add_zone ---

func TestPdnsRecursorAddZone_Success(t *testing.T) {
	_, _, probe, cleanup := newTestCmdManager(t)
	defer cleanup()

	raw := json.RawMessage(`{"zone":"example.com","addr":"127.0.0.1","port":5300}`)
	got, err := pdnsRecursorAddZoneHandler(context.Background(), raw)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	resp, ok := got.(pdnsRecursorAddZoneResponse)
	if !ok {
		t.Fatalf("resp type: %T", got)
	}
	if resp.Zone != "example.com" || !resp.Changed {
		t.Errorf("resp = %+v", resp)
	}
	if len(probe.calls) != 1 || probe.calls[0] != "example.com" {
		t.Errorf("probe calls = %v", probe.calls)
	}
}

func TestPdnsRecursorAddZone_DefaultPort(t *testing.T) {
	m, _, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	raw := json.RawMessage(`{"zone":"example.com","addr":"127.0.0.1"}`) // no port
	if _, err := pdnsRecursorAddZoneHandler(context.Background(), raw); err != nil {
		t.Fatalf("handler: %v", err)
	}
	entries, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Port != 5300 {
		t.Errorf("handler should have defaulted Port=5300, got entries=%+v", entries)
	}
}

func TestPdnsRecursorAddZone_MissingZone(t *testing.T) {
	_, _, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	raw := json.RawMessage(`{"addr":"127.0.0.1","port":5300}`)
	_, err := pdnsRecursorAddZoneHandler(context.Background(), raw)
	if err == nil {
		t.Fatal("should reject missing zone")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) || ae.Code != agentwire.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %v", err)
	}
}

func TestPdnsRecursorAddZone_InvalidZoneIsInvalidArg(t *testing.T) {
	_, _, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	raw := json.RawMessage(`{"zone":"UPPERCASE.COM","addr":"127.0.0.1","port":5300}`)
	_, err := pdnsRecursorAddZoneHandler(context.Background(), raw)
	if err == nil {
		t.Fatal("should reject uppercase zone")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("want AgentError: %v", err)
	}
	if ae.Code != agentwire.CodeInvalidArgument {
		t.Errorf("want CodeInvalidArgument, got Code=%s (msg=%s)", ae.Code, ae.Message)
	}
}

func TestPdnsRecursorAddZone_SelfLoopRejected(t *testing.T) {
	_, _, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	// Forwarder pointing at 127.0.0.1:53 would loop into the recursor itself.
	raw := json.RawMessage(`{"zone":"example.com","addr":"127.0.0.1","port":53}`)
	_, err := pdnsRecursorAddZoneHandler(context.Background(), raw)
	if err == nil {
		t.Fatal("should reject self-loop")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) || ae.Code != agentwire.CodeInvalidArgument {
		t.Errorf("want invalid_argument for self-loop, got %v", err)
	}
	if !strings.Contains(ae.Message, "self-loop") {
		t.Errorf("message should mention self-loop: %q", ae.Message)
	}
}

// --- remove_zone ---

func TestPdnsRecursorRemoveZone_AbsentIsNoop(t *testing.T) {
	_, exec, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	raw := json.RawMessage(`{"zone":"nonexistent.com"}`)
	got, err := pdnsRecursorRemoveZoneHandler(context.Background(), raw)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	resp, ok := got.(pdnsRecursorRemoveZoneResponse)
	if !ok || resp.Changed {
		t.Errorf("resp should report Changed=false, got %+v", got)
	}
	if len(exec.calls) != 0 {
		t.Errorf("remove of absent should not call rec_control, got %v", exec.calls)
	}
}

func TestPdnsRecursorRemoveZone_Present(t *testing.T) {
	_, _, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	// Seed a zone.
	addRaw := json.RawMessage(`{"zone":"example.com","addr":"127.0.0.1","port":5300}`)
	if _, err := pdnsRecursorAddZoneHandler(context.Background(), addRaw); err != nil {
		t.Fatalf("seed add: %v", err)
	}
	// Remove it.
	rmRaw := json.RawMessage(`{"zone":"example.com"}`)
	got, err := pdnsRecursorRemoveZoneHandler(context.Background(), rmRaw)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	resp := got.(pdnsRecursorRemoveZoneResponse)
	if !resp.Changed {
		t.Error("removing existing zone should Changed=true")
	}
}

// --- list ---

func TestPdnsRecursorList(t *testing.T) {
	_, _, _, cleanup := newTestCmdManager(t)
	defer cleanup()

	for _, z := range []string{"zzz.com", "aaa.com", "mmm.com"} {
		raw := json.RawMessage(`{"zone":"` + z + `","addr":"127.0.0.1","port":5300}`)
		if _, err := pdnsRecursorAddZoneHandler(context.Background(), raw); err != nil {
			t.Fatalf("seed %s: %v", z, err)
		}
	}

	got, err := pdnsRecursorListHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	resp := got.(pdnsRecursorListResponse)
	if len(resp.Entries) != 3 {
		t.Fatalf("len=%d want 3", len(resp.Entries))
	}
	// Sorted.
	wantOrder := []string{"aaa.com", "mmm.com", "zzz.com"}
	for i, e := range resp.Entries {
		if e.Zone != wantOrder[i] {
			t.Errorf("resp[%d].Zone=%q want %q", i, e.Zone, wantOrder[i])
		}
	}
}

// --- registry sanity ---

func TestPdnsRecursorHandlersAreRegistered(t *testing.T) {
	// init() in pdns_recursor.go registers on Default. Verify they're there.
	cmds := Default.Commands()
	have := map[string]bool{}
	for _, c := range cmds {
		have[c] = true
	}
	for _, name := range []string{"pdns.recursor_add_zone", "pdns.recursor_remove_zone", "pdns.recursor_list"} {
		if !have[name] {
			t.Errorf("handler %q not registered (Commands()=%v)", name, cmds)
		}
	}
}
