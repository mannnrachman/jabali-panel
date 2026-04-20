package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type systemResolverSetParams struct {
	Resolvers    []string `json:"resolvers"`
	SearchDomain string   `json:"search_domain"`
}

// hostnameRe validates a single RFC-1123 hostname. No multi-label search-path
// support in the UI today — one domain max, to keep the UX simple. Matches
// systemd-resolved's acceptable Domains= entry for the common case.
var hostnameRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// resolverLimits cap what goes into the drop-in so a runaway caller can't
// blow up resolved.conf.d.
const (
	maxResolvers        = 8
	maxSearchDomainLen  = 253
	resolvedRestartWait = 5 * time.Second
)

func systemResolverSetHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p systemResolverSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if err := validateResolvers(p.Resolvers); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if err := validateSearchDomain(p.SearchDomain); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	// Capture the service state at entry so the restart path only runs on
	// hosts that actually run systemd-resolved. If it's inactive, the admin
	// has been told so by the UI ("drop-in will still be written, but
	// changes won't take effect until the service is running") — we honor
	// that contract: persist the drop-in and return. Trying to start an
	// inactive service here would just fail on hosts that use a different
	// resolver stack (NetworkManager, resolvconf, plain /etc/resolv.conf),
	// and the rollback would then make the feature unusable on those hosts.
	wasActive := systemdResolvedActive()

	previous, _ := os.ReadFile(resolvedDropInPath) // #nosec G304 — fixed path
	newContent := renderResolvedDropIn(p.Resolvers, p.SearchDomain)
	if err := atomicWriteResolvedDropIn(resolvedDropInPath, newContent); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("write drop-in: %v", err)}
	}

	if wasActive {
		if err := restartSystemdResolved(ctx); err != nil {
			// Rollback to previous content so the host isn't left with an
			// invalid drop-in keeping resolved in a failed state.
			if previous != nil {
				_ = atomicWriteResolvedDropIn(resolvedDropInPath, previous)
			} else {
				_ = os.Remove(resolvedDropInPath)
			}
			_ = restartSystemdResolved(ctx) // best-effort recover
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeFailedPrecondition,
				Message: fmt.Sprintf("systemd-resolved restart failed; rolled back drop-in: %v", err),
			}
		}
	}

	// Read back to prove the write + restart took. Truth is on disk.
	resolvers, search, source, rerr := readResolverDropIn(resolvedDropInPath)
	if rerr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("verify drop-in: %v", rerr)}
	}
	return systemResolverGetResponse{
		Active:       systemdResolvedActive(),
		Resolvers:    resolvers,
		SearchDomain: search,
		Source:       source,
	}, nil
}

func validateResolvers(resolvers []string) error {
	if len(resolvers) == 0 {
		return fmt.Errorf("at least one resolver required")
	}
	if len(resolvers) > maxResolvers {
		return fmt.Errorf("too many resolvers (max %d)", maxResolvers)
	}
	seen := map[string]bool{}
	for _, r := range resolvers {
		r = strings.TrimSpace(r)
		if r == "" {
			return fmt.Errorf("empty resolver entry")
		}
		addr, err := netip.ParseAddr(r)
		if err != nil {
			return fmt.Errorf("invalid IP %q", r)
		}
		if addr.IsUnspecified() || addr.IsMulticast() {
			return fmt.Errorf("resolver %q must be a usable unicast address", r)
		}
		if seen[r] {
			return fmt.Errorf("duplicate resolver %q", r)
		}
		seen[r] = true
	}
	return nil
}

func validateSearchDomain(d string) error {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil
	}
	if len(d) > maxSearchDomainLen {
		return fmt.Errorf("search domain too long")
	}
	if !hostnameRe.MatchString(d) {
		return fmt.Errorf("invalid search domain %q", d)
	}
	return nil
}

// renderResolvedDropIn produces the drop-in content. systemd-resolved.conf(5)
// specifies DNS= as a space-separated list of addresses and Domains= as a
// space-separated list (single entry is fine). Section header [Resolve] is
// required.
func renderResolvedDropIn(resolvers []string, search string) []byte {
	var b strings.Builder
	b.WriteString("# Managed by jabali-panel — edits via /jabali-admin/settings → DNS.\n")
	b.WriteString("# To revert: remove this file and `systemctl restart systemd-resolved`.\n")
	b.WriteString("[Resolve]\n")
	b.WriteString("DNS=")
	b.WriteString(strings.Join(resolvers, " "))
	b.WriteString("\n")
	if search = strings.TrimSpace(search); search != "" {
		b.WriteString("Domains=")
		b.WriteString(search)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// atomicWriteResolvedDropIn writes content to a sibling temp file and renames
// over the target, so systemd-resolved never sees a half-written file.
var atomicWriteResolvedDropIn = func(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".jabali-resolved-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// restartSystemdResolved runs `systemctl restart systemd-resolved.service`
// and waits up to resolvedRestartWait for the unit to report active. Without
// the poll, a fast-returning restart can leave the service in "activating"
// while the caller already returned success.
var restartSystemdResolved = func(ctx context.Context) error {
	if _, err := runSystemctl(ctx, "restart", "systemd-resolved.service"); err != nil {
		return err
	}
	deadline := time.Now().Add(resolvedRestartWait)
	for time.Now().Before(deadline) {
		if systemdResolvedActive() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("systemd-resolved did not reach active within %s", resolvedRestartWait)
}

func init() {
	Default.Register("system.resolver.set", systemResolverSetHandler)
}
