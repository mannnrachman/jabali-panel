package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// systemctlCapture records every systemctl argv the fake receives, so
// tests can assert the exact sequence of enable/reload calls.
type systemctlCapture struct {
	calls [][]string
	// respond replaces the default 200/nil answer. First match wins.
	respond func(args []string) ([]byte, error)
}

// wireSystemctl swaps runSystemctl for a test fake + restores it on cleanup.
// Returns the capture so tests can read calls after.
func wireSystemctl(t *testing.T, cap *systemctlCapture) {
	t.Helper()
	orig := runSystemctl
	runSystemctl = func(_ context.Context, args ...string) ([]byte, error) {
		cap.calls = append(cap.calls, append([]string(nil), args...))
		if cap.respond != nil {
			return cap.respond(args)
		}
		return nil, nil
	}
	t.Cleanup(func() { runSystemctl = orig })
}

// wireDKIMDir points dkimKeyDirFunc at a test temp dir + restores it.
func wireDKIMDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := dkimKeyDirFunc
	dkimKeyDirFunc = func() string { return dir }
	t.Cleanup(func() { dkimKeyDirFunc = orig })
	return dir
}

// --- domain.email_enable --------------------------------------------

// enableJMAPRoutes is the full JMAP fake-server route map for a
// successful domain.email_enable. Separate helper because three enable
// tests reuse the same shape.
func enableJMAPRoutes(existingDomainID string, createdDomainID string) map[string]jmapHandler {
	queryResult := jmapQueryResult{Total: 0}
	if existingDomainID != "" {
		queryResult.IDs = []string{existingDomainID}
		queryResult.Total = 1
	}
	createResult := jmapSetResult{
		Created: map[string]json.RawMessage{
			"#d1": json.RawMessage(fmt.Sprintf(`{"id":%q}`, createdDomainID)),
		},
	}
	sigCreateResult := jmapSetResult{
		Created: map[string]json.RawMessage{
			"#sig1": json.RawMessage(`{"id":"sig-42"}`),
		},
	}
	return map[string]jmapHandler{
		"x:Domain/query":        jmapHandlerReturning(queryResult),
		"x:Domain/set":          jmapHandlerReturning(createResult),
		"x:DkimSignature/set":   jmapHandlerReturning(sigCreateResult),
	}
}

func TestDomainEmailEnable_FreshDomain_CreatesDomainAndDkimSignature(t *testing.T) {
	dir := wireDKIMDir(t)
	var sysctl systemctlCapture
	wireSystemctl(t, &sysctl)
	srv := newJMAPServer(t, enableJMAPRoutes("", "dom-new-42"))
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := domainEmailEnableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp, ok := got.(domainEmailEnableResponse)
	if !ok {
		t.Fatalf("type: got %T, want domainEmailEnableResponse", got)
	}
	if !resp.Ok {
		t.Error("ok: false, want true")
	}
	if resp.DKIMSelector != "jabali" {
		t.Errorf("selector: got %q, want %q", resp.DKIMSelector, "jabali")
	}
	if !strings.HasPrefix(resp.DKIMPublicKey, "v=DKIM1; k=ed25519; p=") {
		t.Errorf("public key wrong shape: %q", resp.DKIMPublicKey)
	}

	// Keyfile should exist, mode 0600.
	keyPath := filepath.Join(dir, "example.com.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat keyfile: %v", err)
	}
	if mode := fi.Mode() & os.ModePerm; mode != 0o600 {
		t.Errorf("mode: got %o, want 0600", mode)
	}

	// Three systemctl calls: enable stalwart, enable webmail, reload stalwart.
	if len(sysctl.calls) != 3 {
		t.Fatalf("systemctl calls: got %d, want 3 (%v)", len(sysctl.calls), sysctl.calls)
	}
	wantArgs := [][]string{
		{"enable", "--now", "jabali-stalwart.service"},
		{"enable", "--now", "jabali-webmail.service"},
		{"reload", "jabali-stalwart.service"},
	}
	for i := range wantArgs {
		if strings.Join(sysctl.calls[i], " ") != strings.Join(wantArgs[i], " ") {
			t.Errorf("call %d: got %v, want %v", i, sysctl.calls[i], wantArgs[i])
		}
	}
}

func TestDomainEmailEnable_AlreadyRegistered_SkipsDomainAndSignatureCreates(t *testing.T) {
	// Domain already exists in Stalwart's registry (operator called
	// email_enable twice without a disable in between, or panel
	// reconciler is re-converging). Handler must detect the existing
	// domain and NOT re-create Domain or DkimSignature. The fake's
	// route map deliberately omits x:Domain/set + x:DkimSignature/set
	// — a stray create call would 400 (no such route) and fail the test.
	wireDKIMDir(t)
	var sysctl systemctlCapture
	wireSystemctl(t, &sysctl)
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Domain/query": jmapHandlerReturning(jmapQueryResult{
			IDs: []string{"dom-existing"}, Total: 1,
		}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := domainEmailEnableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := got.(domainEmailEnableResponse)
	if !resp.Ok {
		t.Error("ok: false, want true")
	}
}

func TestEnsureDKIMKey_ReusesExistingKey(t *testing.T) {
	// Stability test against the pure helper — handler can't reach its
	// response path while task-13 refusal is in force, so we test the
	// DKIM re-derivation guarantee at the helper level.
	dir := wireDKIMDir(t)
	keyPath := filepath.Join(dir, "example.com.key")

	firstTXT, err := ensureDKIMKey(keyPath)
	if err != nil {
		t.Fatalf("first ensureDKIMKey: %v", err)
	}
	if !strings.HasPrefix(firstTXT, "v=DKIM1; k=ed25519; p=") {
		t.Errorf("public key wrong shape: %q", firstTXT)
	}
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read keyfile: %v", err)
	}

	secondTXT, err := ensureDKIMKey(keyPath)
	if err != nil {
		t.Fatalf("second ensureDKIMKey: %v", err)
	}
	if firstTXT != secondTXT {
		t.Errorf("TXT drift between first + second derivation\n first: %s\nsecond: %s", firstTXT, secondTXT)
	}

	// Keyfile bytes must be byte-identical (no rewrite on re-derivation).
	keyAfter, _ := os.ReadFile(keyPath)
	if string(keyBefore) != string(keyAfter) {
		t.Error("keyfile rewritten on second call — DKIM key must be stable")
	}
}

func TestDomainEmailEnable_BadDomainNameRejected(t *testing.T) {
	wireDKIMDir(t)
	var sysctl systemctlCapture
	wireSystemctl(t, &sysctl)

	cases := []string{
		`{"domain_id":"01J","domain_name":"exa mple.com"}`, // space
		`{"domain_id":"01J","domain_name":"ex;.com"}`,      // semicolon
		`{"domain_id":"01J","domain_name":"../etc"}`,       // path traversal via slash
		`{"domain_id":"01J","domain_name":".bad"}`,         // leading dot
		`{"domain_id":"01J","domain_name":"-bad.com"}`,     // leading hyphen
		`{"domain_id":"01J","domain_name":""}`,             // empty
	}
	for _, params := range cases {
		_, err := domainEmailEnableHandler(context.Background(), json.RawMessage(params))
		requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
	}

	// No systemctl calls should have fired for any rejected input.
	if len(sysctl.calls) != 0 {
		t.Errorf("systemctl should not have been invoked for rejected inputs, got %d calls", len(sysctl.calls))
	}
}

func TestDomainEmailEnable_SystemctlEnableFailure(t *testing.T) {
	// systemctl enable fails BEFORE any JMAP call — no JMAP fake needed
	// because the handler errors out before reaching the Domain/query.
	wireDKIMDir(t)
	sysctl := &systemctlCapture{
		respond: func(args []string) ([]byte, error) {
			if len(args) >= 3 && args[0] == "enable" && args[2] == "jabali-stalwart.service" {
				return []byte("some systemctl output"), errors.New("unit failed")
			}
			return nil, nil
		},
	}
	wireSystemctl(t, sysctl)

	_, err := domainEmailEnableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	requireAgentErrorCode(t, err, agentwire.CodeInternal)
}

func TestDomainEmailEnable_ReloadNotSupportedIsNotFatal(t *testing.T) {
	// If Stalwart's unit doesn't declare ExecReload, systemctl reload
	// returns a "reload is not applicable" error. The handler swallows
	// it (per isReloadNotSupportedErr) and proceeds to the JMAP
	// Domain + DkimSignature creates. Re-verify that the handler
	// still acks ok when only the reload fails this way.
	wireDKIMDir(t)
	sysctl := &systemctlCapture{
		respond: func(args []string) ([]byte, error) {
			if len(args) >= 1 && args[0] == "reload" {
				return []byte("Failed to reload jabali-stalwart.service: Job type reload is not applicable for unit jabali-stalwart.service."),
					errors.New("exit status 1")
			}
			return nil, nil
		},
	}
	wireSystemctl(t, sysctl)
	srv := newJMAPServer(t, enableJMAPRoutes("", "dom-reload-42"))
	defer srv.Close()
	wireJMAP(t, srv)

	_, err := domainEmailEnableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	if err != nil {
		t.Fatalf("reload-not-applicable should not fail: %v", err)
	}
}

// --- domain.email_disable --------------------------------------------
//
// v0.16 flow: agent calls JMAP Domain/query (resolve id) then Domain/set
// destroy, removes the DKIM keyfile, reloads Stalwart. Happy path
// therefore requires a JMAP fake in addition to the systemctl capture.

func TestDomainEmailDisable_DestroysRegistryAndRemovesKeyAndReloads(t *testing.T) {
	dir := wireDKIMDir(t)
	var sysctl systemctlCapture
	wireSystemctl(t, &sysctl)

	// JMAP fake: answer Domain/query with an id, Domain/set destroy ok.
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Domain/query": jmapHandlerReturning(jmapQueryResult{
			IDs: []string{"dom-42"}, Total: 1,
		}),
		"x:Domain/set": jmapHandlerReturning(jmapSetResult{Destroyed: []string{"dom-42"}}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	// Seed a keyfile to be removed.
	keyPath := filepath.Join(dir, "example.com.key")
	if err := os.WriteFile(keyPath, []byte("seed"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := domainEmailDisableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("keyfile should be gone, stat err: %v", err)
	}
	// Exactly one systemctl call: reload.
	if len(sysctl.calls) != 1 {
		t.Fatalf("systemctl calls: got %d, want 1 (%v)", len(sysctl.calls), sysctl.calls)
	}
	if strings.Join(sysctl.calls[0], " ") != "reload jabali-stalwart.service" {
		t.Errorf("reload argv: got %v, want [reload jabali-stalwart.service]", sysctl.calls[0])
	}
}

func TestDomainEmailDisable_NeverSynced_SkipsDestroyCall(t *testing.T) {
	// Domain not in registry (enable was never called for this host's
	// Stalwart). Query returns no ids, destroy route is not hit — the
	// route map deliberately omits Domain/set to catch an unexpected call.
	wireDKIMDir(t)
	var sysctl systemctlCapture
	wireSystemctl(t, &sysctl)

	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Domain/query": jmapHandlerReturning(jmapQueryResult{IDs: nil, Total: 0}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	_, err := domainEmailDisableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	if err != nil {
		t.Fatalf("never-synced disable should succeed: %v", err)
	}
	if len(sysctl.calls) != 1 {
		t.Errorf("expected 1 systemctl call (reload), got %d", len(sysctl.calls))
	}
}

func TestDomainEmailDisable_IdempotentOnMissingKey(t *testing.T) {
	wireDKIMDir(t) // fresh tempdir, no keyfile
	var sysctl systemctlCapture
	wireSystemctl(t, &sysctl)

	// JMAP fake: destroy path not exercised (registry empty).
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Domain/query": jmapHandlerReturning(jmapQueryResult{IDs: nil, Total: 0}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	_, err := domainEmailDisableHandler(context.Background(), json.RawMessage(
		`{"domain_id":"01J","domain_name":"example.com"}`))
	if err != nil {
		t.Fatalf("missing-keyfile disable should succeed: %v", err)
	}
}

func TestDomainEmailDisable_BadParams(t *testing.T) {
	_, err := domainEmailDisableHandler(context.Background(), json.RawMessage(`{}`))
	requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
}
