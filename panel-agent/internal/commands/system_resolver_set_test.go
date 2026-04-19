package commands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// stubRestartResolved replaces restartSystemdResolved for the test; pass
// err=nil for a successful restart simulation.
func stubRestartResolved(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	old := restartSystemdResolved
	restartSystemdResolved = func(_ context.Context) error {
		calls++
		return err
	}
	t.Cleanup(func() { restartSystemdResolved = old })
	return &calls
}

func TestSystemResolverSet_ValidIPv4AndIPv6(t *testing.T) {
	path := setResolverTestPath(t)
	stubResolvedActive(t, true)
	stubRestartResolved(t, nil)

	params := json.RawMessage(`{"resolvers":["1.1.1.1","1.0.0.1","2606:4700:4700::1111"],"search_domain":"example.com"}`)
	resp, err := systemResolverSetHandler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got := resp.(systemResolverGetResponse)
	if got.Source != "drop-in" {
		t.Errorf("source = %q, want drop-in", got.Source)
	}
	if len(got.Resolvers) != 3 {
		t.Errorf("resolvers = %v, want 3", got.Resolvers)
	}
	if got.SearchDomain != "example.com" {
		t.Errorf("search = %q, want example.com", got.SearchDomain)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "[Resolve]") {
		t.Errorf("missing [Resolve] section header in %s", s)
	}
	if !strings.Contains(s, "DNS=1.1.1.1 1.0.0.1 2606:4700:4700::1111") {
		t.Errorf("missing DNS= line in %s", s)
	}
	if !strings.Contains(s, "Domains=example.com") {
		t.Errorf("missing Domains= line in %s", s)
	}
}

func TestSystemResolverSet_EmptySearchDomainOmitsLine(t *testing.T) {
	path := setResolverTestPath(t)
	stubResolvedActive(t, true)
	stubRestartResolved(t, nil)

	params := json.RawMessage(`{"resolvers":["8.8.8.8"],"search_domain":""}`)
	if _, err := systemResolverSetHandler(context.Background(), params); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	s, _ := os.ReadFile(path)
	if strings.Contains(string(s), "Domains=") {
		t.Errorf("expected no Domains= line when empty, got %s", s)
	}
}

func TestSystemResolverSet_InvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantMsg string
	}{
		{"empty_body", ``, "params required"},
		{"malformed_json", `{broken`, "parse params"},
		{"empty_resolvers", `{"resolvers":[]}`, "at least one"},
		{"too_many_resolvers", `{"resolvers":["1.1.1.1","1.0.0.1","8.8.8.8","8.8.4.4","9.9.9.9","149.112.112.112","208.67.222.222","208.67.220.220","76.76.2.0"]}`, "too many"},
		{"invalid_ip", `{"resolvers":["not-an-ip"]}`, "invalid IP"},
		{"unspecified_ip", `{"resolvers":["0.0.0.0"]}`, "unicast"},
		{"multicast_ip", `{"resolvers":["224.0.0.1"]}`, "unicast"},
		{"duplicate", `{"resolvers":["1.1.1.1","1.1.1.1"]}`, "duplicate"},
		{"bad_search_domain", `{"resolvers":["1.1.1.1"],"search_domain":"not a domain"}`, "invalid search domain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setResolverTestPath(t)
			stubResolvedActive(t, true)
			stubRestartResolved(t, nil)

			_, err := systemResolverSetHandler(context.Background(), json.RawMessage(tc.payload))
			if err == nil {
				t.Fatal("expected error")
			}
			ae, ok := err.(*agentwire.AgentError)
			if !ok {
				t.Fatalf("expected AgentError, got %T: %v", err, err)
			}
			if ae.Code != agentwire.CodeInvalidArgument {
				t.Errorf("code = %q, want %q", ae.Code, agentwire.CodeInvalidArgument)
			}
			if !strings.Contains(ae.Message, tc.wantMsg) {
				t.Errorf("message = %q, want contains %q", ae.Message, tc.wantMsg)
			}
		})
	}
}

// TestSystemResolverSet_RollbackOnRestartFailure — if systemd-resolved fails
// to come back up, the previous drop-in content must be restored so the host
// isn't left with a broken resolver config.
func TestSystemResolverSet_RollbackOnRestartFailure(t *testing.T) {
	path := setResolverTestPath(t)
	stubResolvedActive(t, true)

	prev := []byte("[Resolve]\nDNS=8.8.8.8\n")
	if err := os.WriteFile(path, prev, 0o644); err != nil {
		t.Fatalf("seed prev: %v", err)
	}

	calls := stubRestartResolved(t, errors.New("resolved refused to start"))

	params := json.RawMessage(`{"resolvers":["1.1.1.1"],"search_domain":""}`)
	_, err := systemResolverSetHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error from failed restart")
	}
	ae, ok := err.(*agentwire.AgentError)
	if !ok {
		t.Fatalf("expected AgentError, got %T", err)
	}
	if ae.Code != agentwire.CodeFailedPrecondition {
		t.Errorf("code = %q, want failed_precondition", ae.Code)
	}

	// The drop-in should have been reverted to the previous content.
	got, _ := os.ReadFile(path)
	if string(got) != string(prev) {
		t.Errorf("drop-in not rolled back\nwant:\n%s\ngot:\n%s", prev, got)
	}
	// One apply + one rollback restart = 2 calls.
	if *calls != 2 {
		t.Errorf("restart calls = %d, want 2 (apply + rollback)", *calls)
	}
}

// TestSystemResolverSet_RollbackWhenNoPriorDropIn — when there was no prior
// file, rollback must delete the new one rather than restoring stale state.
func TestSystemResolverSet_RollbackWhenNoPriorDropIn(t *testing.T) {
	path := setResolverTestPath(t)
	stubResolvedActive(t, true)
	stubRestartResolved(t, errors.New("nope"))

	params := json.RawMessage(`{"resolvers":["1.1.1.1"]}`)
	if _, err := systemResolverSetHandler(context.Background(), params); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected drop-in removed after rollback, stat err=%v", err)
	}
}

func TestSystemResolverSet_Registered(t *testing.T) {
	t.Parallel()
	for _, name := range Default.Commands() {
		if name == "system.resolver.set" {
			return
		}
	}
	t.Fatal("system.resolver.set not registered")
}

func TestValidateSearchDomain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"example.com", false},
		{"sub.example.co.uk", false},
		{"a", false},
		{"foo bar", true},
		{"-foo.com", true},
		{"foo_bar.com", true},
		{strings.Repeat("a", 254), true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := validateSearchDomain(tc.in)
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
