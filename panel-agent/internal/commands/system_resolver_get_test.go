package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setResolverTestPath points the handler at a temp drop-in so we don't touch
// /etc/systemd/resolved.conf.d on the dev box.
func setResolverTestPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "jabali.conf")
	old := resolvedDropInPath
	resolvedDropInPath = path
	t.Cleanup(func() { resolvedDropInPath = old })
	return path
}

// stubResolvedActive replaces systemdResolvedActive for the duration of the
// test so we don't shell out to systemctl.
func stubResolvedActive(t *testing.T, active bool) {
	t.Helper()
	old := systemdResolvedActive
	systemdResolvedActive = func() bool { return active }
	t.Cleanup(func() { systemdResolvedActive = old })
}

func TestSystemResolverGet_AbsentFile(t *testing.T) {
	setResolverTestPath(t)
	stubResolvedActive(t, true)

	resp, err := systemResolverGetHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got, ok := resp.(systemResolverGetResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", resp)
	}
	if got.Source != "none" {
		t.Errorf("source = %q, want %q", got.Source, "none")
	}
	if len(got.Resolvers) != 0 {
		t.Errorf("resolvers = %v, want empty", got.Resolvers)
	}
	if got.SearchDomain != "" {
		t.Errorf("search = %q, want empty", got.SearchDomain)
	}
	if !got.Active {
		t.Errorf("active = false, want true")
	}
}

func TestSystemResolverGet_ParsesDropIn(t *testing.T) {
	path := setResolverTestPath(t)
	stubResolvedActive(t, true)

	content := "# Managed\n[Resolve]\nDNS=1.1.1.1 1.0.0.1 2606:4700:4700::1111\nDomains=example.com\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	resp, err := systemResolverGetHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got := resp.(systemResolverGetResponse)
	if got.Source != "drop-in" {
		t.Errorf("source = %q, want drop-in", got.Source)
	}
	wantResolvers := []string{"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111"}
	if len(got.Resolvers) != len(wantResolvers) {
		t.Fatalf("resolvers = %v, want %v", got.Resolvers, wantResolvers)
	}
	for i, r := range wantResolvers {
		if got.Resolvers[i] != r {
			t.Errorf("resolvers[%d] = %q, want %q", i, got.Resolvers[i], r)
		}
	}
	if got.SearchDomain != "example.com" {
		t.Errorf("search = %q, want example.com", got.SearchDomain)
	}
}

func TestSystemResolverGet_SkipsMalformedLines(t *testing.T) {
	path := setResolverTestPath(t)
	stubResolvedActive(t, false)

	// Invalid IP in DNS= list, garbage line, commented line — all tolerated.
	content := "[Resolve]\nDNS=8.8.8.8 not-an-ip ::1\n# Comment\ngarbage without equals\nDomains=\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	resp, err := systemResolverGetHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got := resp.(systemResolverGetResponse)
	want := []string{"8.8.8.8", "::1"}
	if len(got.Resolvers) != len(want) {
		t.Fatalf("resolvers = %v, want %v", got.Resolvers, want)
	}
	for i, r := range want {
		if got.Resolvers[i] != r {
			t.Errorf("resolvers[%d] = %q, want %q", i, got.Resolvers[i], r)
		}
	}
	if got.Active {
		t.Errorf("active = true, want false")
	}
}

func TestSystemResolverGet_Registered(t *testing.T) {
	t.Parallel()
	for _, name := range Default.Commands() {
		if name == "system.resolver.get" {
			return
		}
	}
	t.Fatal("system.resolver.get not registered")
}

// TestSystemResolverGet_JSONShape freezes the wire contract for the panel.
// Any change to the field names here is a cross-boundary break.
func TestSystemResolverGet_JSONShape(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(systemResolverGetResponse{
		Active:       true,
		Resolvers:    []string{"1.1.1.1"},
		SearchDomain: "example.com",
		Source:       "drop-in",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"active", "resolvers", "search_domain", "source"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q in %s", key, raw)
		}
	}
}
