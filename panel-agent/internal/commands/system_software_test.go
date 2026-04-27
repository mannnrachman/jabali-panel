package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"nginx version: nginx/1.24.0\n", "1.24.0"},
		{"PHP 8.4.16 (cli) (built: ...)\n", "8.4.16"},
		{"v22.11.0\n", "22.11.0"},
		{"mariadb  Ver 15.1 Distrib 11.4.8-MariaDB", "15.1"},
		{"Redis server v=7.0.15 sha=00000000:0", "7.0.15"},
		{"OpenSSH_9.2p1 Debian-2+deb12u3, OpenSSL 3.0.16", "9.2"},
		{"no version here", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := parseVersion(c.in)
		if got != c.want {
			t.Errorf("parseVersion(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSystemSoftwareHandlerCaches(t *testing.T) {
	resetSoftwareCacheForTest()
	t.Cleanup(resetSoftwareCacheForTest)

	first, err := systemSoftwareHandler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r1, ok := first.(SystemSoftwareResponse)
	if !ok {
		t.Fatalf("unexpected type %T", first)
	}
	if len(r1.Items) != len(softwareProbes) {
		t.Errorf("expected %d items, got %d", len(softwareProbes), len(r1.Items))
	}

	// Cache should be hot — second call returns instantly without
	// re-running probes. We can't easily mock exec, so just assert
	// the response object is identical and TTL is in the future.
	softwareCacheMu.Lock()
	exp := softwareCacheExpires
	softwareCacheMu.Unlock()
	if exp.Before(time.Now()) {
		t.Error("expected cache TTL in the future after first call")
	}

	second, err := systemSoftwareHandler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	r2 := second.(SystemSoftwareResponse)
	if len(r2.Items) != len(r1.Items) {
		t.Fatalf("cached response item count mismatch")
	}
	for i := range r1.Items {
		if r1.Items[i] != r2.Items[i] {
			t.Errorf("item %d mismatch on cached call: %+v vs %+v", i, r1.Items[i], r2.Items[i])
		}
	}
}

func TestSystemSoftwareProbeNamesNonEmpty(t *testing.T) {
	for _, p := range softwareProbes {
		if strings.TrimSpace(p.name) == "" {
			t.Errorf("probe %s has empty name", p.bin)
		}
		if strings.TrimSpace(p.bin) == "" {
			t.Errorf("probe %s has empty bin", p.name)
		}
	}
}
