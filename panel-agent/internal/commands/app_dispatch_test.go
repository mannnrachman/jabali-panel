package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// withCleanDispatchTables snapshots all three dispatch tables, clears
// them, and registers a t.Cleanup to restore the snapshot. Subsequent
// tests in the same binary rely on the package-init wordpress
// registration being intact, so per-test isolation MUST be matched by
// per-test restoration of every table — not just the one the test
// touched.
func withCleanDispatchTables(t *testing.T) {
	t.Helper()
	prevI := snapshotInstallers()
	prevD := snapshotDeleters()
	prevC := snapshotCloners()
	appDispatchMu.Lock()
	appInstallers = map[string]Handler{}
	appDeleters = map[string]Handler{}
	appCloners = map[string]Handler{}
	appDispatchMu.Unlock()
	t.Cleanup(func() {
		restoreInstallers(prevI)
		restoreDeleters(prevD)
		restoreCloners(prevC)
	})
}

func TestAppInstall_DispatchesByAppType(t *testing.T) {
	withCleanDispatchTables(t)
	called := ""
	RegisterAppInstaller("demo", func(ctx context.Context, params json.RawMessage) (any, error) {
		called = "demo"
		return map[string]string{"ok": "demo"}, nil
	})

	body := json.RawMessage(`{"app_type":"demo","field":1}`)
	out, err := appInstallHandler(context.Background(), body)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if called != "demo" {
		t.Fatalf("wrong handler invoked: %q", called)
	}
	if m, ok := out.(map[string]string); !ok || m["ok"] != "demo" {
		t.Fatalf("response: %v", out)
	}
}

func TestAppInstall_RejectsUnknownAppType(t *testing.T) {
	withCleanDispatchTables(t)

	body := json.RawMessage(`{"app_type":"nope"}`)
	_, err := appInstallHandler(context.Background(), body)
	if err == nil {
		t.Fatal("expected error for unknown app_type")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("want *agentwire.AgentError, got %T", err)
	}
	if ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("code = %q", ae.Code)
	}
	if !strings.Contains(ae.Message, "unknown app_type") {
		t.Fatalf("msg = %q", ae.Message)
	}
}

func TestAppInstall_RejectsMissingAppType(t *testing.T) {
	body := json.RawMessage(`{"field":1}`)
	_, err := appInstallHandler(context.Background(), body)
	if err == nil {
		t.Fatal("expected error for missing app_type")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("want *agentwire.AgentError, got %T", err)
	}
	if ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("code = %q", ae.Code)
	}
}

func TestAppInstall_RejectsBadJSON(t *testing.T) {
	body := json.RawMessage(`not-json`)
	_, err := appInstallHandler(context.Background(), body)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("want *agentwire.AgentError, got %T", err)
	}
}

func TestRegisterAppInstaller_PanicsOnDuplicate(t *testing.T) {
	withCleanDispatchTables(t)

	noop := func(ctx context.Context, p json.RawMessage) (any, error) { return nil, nil }
	RegisterAppInstaller("demo", noop)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	RegisterAppInstaller("demo", noop)
}

func TestAppDelete_RoutesByAppType(t *testing.T) {
	withCleanDispatchTables(t)

	hit := false
	RegisterAppDeleter("demo", func(ctx context.Context, p json.RawMessage) (any, error) {
		hit = true
		return nil, nil
	})
	if _, err := appDeleteHandler(context.Background(), json.RawMessage(`{"app_type":"demo"}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !hit {
		t.Fatal("delete handler not invoked")
	}
}

func TestAppClone_RoutesByAppType(t *testing.T) {
	withCleanDispatchTables(t)

	hit := false
	RegisterAppCloner("demo", func(ctx context.Context, p json.RawMessage) (any, error) {
		hit = true
		return nil, nil
	})
	if _, err := appCloneHandler(context.Background(), json.RawMessage(`{"app_type":"demo"}`)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !hit {
		t.Fatal("clone handler not invoked")
	}
}

func TestDefaultRegistry_HasAppCommands(t *testing.T) {
	want := map[string]bool{"app.install": false, "app.delete": false, "app.clone": false}
	for _, c := range Default.Commands() {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for cmd, found := range want {
		if !found {
			t.Errorf("Default registry missing %q", cmd)
		}
	}
}

func TestDefaultRegistry_StillHasLegacyWordPressCommands(t *testing.T) {
	// Legacy commands stay registered through M19 so a stale panel
	// build can keep dispatching to them. Removed in M19.1.
	want := map[string]bool{"wordpress.install": false, "wordpress.delete": false, "wordpress.clone": false}
	for _, c := range Default.Commands() {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for cmd, found := range want {
		if !found {
			t.Errorf("Default registry missing legacy %q", cmd)
		}
	}
}

// snapshot/restore helpers let individual tests reset the dispatch
// tables without affecting the package-init wordpress registration
// that other tests in the binary may rely on.
func snapshotInstallers() map[string]Handler { return cloneMap(appInstallers) }
func snapshotDeleters() map[string]Handler   { return cloneMap(appDeleters) }
func snapshotCloners() map[string]Handler    { return cloneMap(appCloners) }

func restoreInstallers(m map[string]Handler) { appDispatchMu.Lock(); appInstallers = m; appDispatchMu.Unlock() }
func restoreDeleters(m map[string]Handler)   { appDispatchMu.Lock(); appDeleters = m; appDispatchMu.Unlock() }
func restoreCloners(m map[string]Handler)    { appDispatchMu.Lock(); appCloners = m; appDispatchMu.Unlock() }

func cloneMap(in map[string]Handler) map[string]Handler {
	appDispatchMu.RLock()
	defer appDispatchMu.RUnlock()
	out := make(map[string]Handler, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
