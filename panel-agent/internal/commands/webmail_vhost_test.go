package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reloadCapture counts how many times the nginx reload shim was invoked.
// Tests swap it in for defaultNginxTestAndReload so the agent code paths
// don't need a real nginx binary.
type reloadCapture struct {
	calls int
	fail  error
}

func wireNginxReload(t *testing.T) *reloadCapture {
	t.Helper()
	orig := nginxTestAndReload
	cap := &reloadCapture{}
	nginxTestAndReload = func(_ context.Context) error {
		cap.calls++
		return cap.fail
	}
	t.Cleanup(func() { nginxTestAndReload = orig })
	return cap
}

// wireMailVhostPaths points the sites-available / sites-enabled dirs
// at test temp dirs so the agent writes don't touch the real nginx
// tree. Returns (available, enabled).
func wireMailVhostPaths(t *testing.T) (string, string) {
	t.Helper()
	avail := filepath.Join(t.TempDir(), "sites-available")
	enabl := filepath.Join(t.TempDir(), "sites-enabled")
	if err := os.MkdirAll(avail, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(enabl, 0755); err != nil {
		t.Fatal(err)
	}
	origA, origE := mailVhostSitesAvailable, mailVhostSitesEnabled
	mailVhostSitesAvailable = avail
	mailVhostSitesEnabled = enabl
	t.Cleanup(func() {
		mailVhostSitesAvailable = origA
		mailVhostSitesEnabled = origE
	})
	return avail, enabl
}

func TestWebmailVhostApply_WritesAndReloads(t *testing.T) {
	avail, enabl := wireMailVhostPaths(t)
	cap := wireNginxReload(t)

	params, _ := json.Marshal(webmailVhostApplyParams{
		DomainName:  "example.com",
		SSLCertPath: "/etc/letsencrypt/live/example.com/fullchain.pem",
		SSLKeyPath:  "/etc/letsencrypt/live/example.com/privkey.pem",
	})
	got, err := webmailVhostApplyHandler(context.Background(), params)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	resp := got.(webmailVhostResponse)
	if !resp.Ok || !resp.Changed {
		t.Errorf("expected Ok=true, Changed=true, got %+v", resp)
	}
	// Vhost file exists with expected contents.
	confPath := filepath.Join(avail, "example.com-mail.conf")
	b, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read vhost: %v", err)
	}
	if !strings.Contains(string(b), "server_name mail.example.com autoconfig.example.com;") {
		t.Errorf("vhost missing server_name: %s", string(b))
	}
	if !strings.Contains(string(b), "/etc/letsencrypt/live/example.com/fullchain.pem") {
		t.Errorf("vhost missing cert path substitution: %s", string(b))
	}
	// M25 Step 5: Bulwark moved off TCP 127.0.0.1:3000 onto a Unix
	// socket fronted by the named upstream `jabali_bulwark`.
	if !strings.Contains(string(b), "proxy_pass http://jabali_bulwark/") {
		t.Error("vhost missing Bulwark proxy_pass to jabali_bulwark upstream")
	}
	if !strings.Contains(string(b), "proxy_pass http://127.0.0.1:8446") {
		t.Error("vhost missing Stalwart proxy_pass")
	}
	// Enabled symlink exists and points at sites-available.
	target, err := os.Readlink(filepath.Join(enabl, "example.com-mail.conf"))
	if err != nil {
		t.Fatalf("readlink enabled: %v", err)
	}
	if target != confPath {
		t.Errorf("symlink target = %q, want %q", target, confPath)
	}
	if cap.calls != 1 {
		t.Errorf("nginx reload should fire exactly once, got %d", cap.calls)
	}
}

// TestWebmailVhostApply_IdempotentSameContent — second apply with
// identical params must not write the file again AND must not reload
// nginx. This is the reconciler's steady-state case, called every tick.
func TestWebmailVhostApply_IdempotentSameContent(t *testing.T) {
	wireMailVhostPaths(t)
	cap := wireNginxReload(t)

	params, _ := json.Marshal(webmailVhostApplyParams{
		DomainName:  "example.com",
		SSLCertPath: "/ssl/cert", SSLKeyPath: "/ssl/key",
	})
	if _, err := webmailVhostApplyHandler(context.Background(), params); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := webmailVhostApplyHandler(context.Background(), params); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if cap.calls != 1 {
		t.Errorf("nginx reload should fire only on the first apply, got %d calls", cap.calls)
	}
}

// TestWebmailVhostApply_ReLinksDanglingSymlink — if a prior vhost_remove
// blew away the symlink but the file is still there, the next apply
// restores the symlink and reloads.
func TestWebmailVhostApply_ReLinksDanglingSymlink(t *testing.T) {
	_, enabl := wireMailVhostPaths(t)
	cap := wireNginxReload(t)

	params, _ := json.Marshal(webmailVhostApplyParams{
		DomainName:  "example.com",
		SSLCertPath: "/ssl/cert", SSLKeyPath: "/ssl/key",
	})
	if _, err := webmailVhostApplyHandler(context.Background(), params); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// Remove the symlink only.
	if err := os.Remove(filepath.Join(enabl, "example.com-mail.conf")); err != nil {
		t.Fatal(err)
	}
	got, err := webmailVhostApplyHandler(context.Background(), params)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !got.(webmailVhostResponse).Changed {
		t.Error("second apply should report Changed=true to signal the symlink repair")
	}
	if cap.calls != 2 {
		t.Errorf("nginx reload should fire twice (first write + symlink repair), got %d", cap.calls)
	}
	if _, err := os.Lstat(filepath.Join(enabl, "example.com-mail.conf")); err != nil {
		t.Errorf("symlink should have been re-created: %v", err)
	}
}

// TestWebmailVhostApply_RollbackOnNginxFailure — if nginx -t or reload
// fails, the vhost file AND the symlink must be removed so subsequent
// reloads (by other code paths) don't trip over the bad config.
func TestWebmailVhostApply_RollbackOnNginxFailure(t *testing.T) {
	avail, enabl := wireMailVhostPaths(t)
	cap := wireNginxReload(t)
	cap.fail = errors.New("nginx -t: syntax error")

	params, _ := json.Marshal(webmailVhostApplyParams{
		DomainName:  "example.com",
		SSLCertPath: "/ssl/cert", SSLKeyPath: "/ssl/key",
	})
	_, err := webmailVhostApplyHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected apply to fail when nginx reload fails")
	}
	if _, err := os.Stat(filepath.Join(avail, "example.com-mail.conf")); !os.IsNotExist(err) {
		t.Error("sites-available file must be removed on rollback")
	}
	if _, err := os.Lstat(filepath.Join(enabl, "example.com-mail.conf")); !os.IsNotExist(err) {
		t.Error("sites-enabled symlink must be removed on rollback")
	}
}

// TestWebmailVhostApply_RejectsShellMetaInDomainName — the domain name
// ends up in `server_name` which nginx parses, but it also forms the
// file path. Must go through validateDomainNameForShell to block
// traversal attempts.
func TestWebmailVhostApply_RejectsShellMetaInDomainName(t *testing.T) {
	wireMailVhostPaths(t)
	wireNginxReload(t)

	cases := []string{
		"example.com;rm",
		"../etc/passwd",
		"foo$(bar)",
		"",
		"foo bar.com",
	}
	for _, name := range cases {
		params, _ := json.Marshal(webmailVhostApplyParams{
			DomainName:  name,
			SSLCertPath: "/ssl/cert", SSLKeyPath: "/ssl/key",
		})
		if _, err := webmailVhostApplyHandler(context.Background(), params); err == nil {
			t.Errorf("expected reject for domain %q, got nil", name)
		}
	}
}

// TestWebmailVhostApply_RejectsMissingSSLPaths — agent must not write
// a vhost that would crash nginx with "ssl_certificate path is empty".
func TestWebmailVhostApply_RejectsMissingSSLPaths(t *testing.T) {
	wireMailVhostPaths(t)
	wireNginxReload(t)

	params, _ := json.Marshal(webmailVhostApplyParams{
		DomainName:  "example.com",
		SSLCertPath: "", SSLKeyPath: "/ssl/key",
	})
	if _, err := webmailVhostApplyHandler(context.Background(), params); err == nil {
		t.Error("expected reject when ssl_cert_path empty")
	}
}

// TestWebmailVhostRemove_RemovesBothFilesAndReloads — happy path.
func TestWebmailVhostRemove_RemovesBothFilesAndReloads(t *testing.T) {
	avail, enabl := wireMailVhostPaths(t)
	cap := wireNginxReload(t)

	applyParams, _ := json.Marshal(webmailVhostApplyParams{
		DomainName: "example.com", SSLCertPath: "/ssl/cert", SSLKeyPath: "/ssl/key",
	})
	if _, err := webmailVhostApplyHandler(context.Background(), applyParams); err != nil {
		t.Fatalf("setup apply: %v", err)
	}
	cap.calls = 0

	removeParams, _ := json.Marshal(webmailVhostRemoveParams{DomainName: "example.com"})
	got, err := webmailVhostRemoveHandler(context.Background(), removeParams)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !got.(webmailVhostResponse).Changed {
		t.Error("remove should report Changed=true when files existed")
	}
	if _, err := os.Stat(filepath.Join(avail, "example.com-mail.conf")); !os.IsNotExist(err) {
		t.Error("sites-available file must be gone")
	}
	if _, err := os.Lstat(filepath.Join(enabl, "example.com-mail.conf")); !os.IsNotExist(err) {
		t.Error("sites-enabled symlink must be gone")
	}
	if cap.calls != 1 {
		t.Errorf("nginx reload must fire once on real remove, got %d", cap.calls)
	}
}

// TestWebmailVhostRemove_NoopWhenAbsent — calling remove on a never-
// applied domain returns ok+Changed=false without reloading nginx.
// This matters because the reconciler calls remove on every disabled
// domain every tick; cheap no-op is required.
func TestWebmailVhostRemove_NoopWhenAbsent(t *testing.T) {
	wireMailVhostPaths(t)
	cap := wireNginxReload(t)

	removeParams, _ := json.Marshal(webmailVhostRemoveParams{DomainName: "unused.example.com"})
	got, err := webmailVhostRemoveHandler(context.Background(), removeParams)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got.(webmailVhostResponse).Changed {
		t.Error("remove on absent files should report Changed=false")
	}
	if cap.calls != 0 {
		t.Errorf("nginx reload must NOT fire for no-op remove, got %d", cap.calls)
	}
}
