package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// systemd-resolved drop-in path the panel owns. Other drop-ins placed by
// packages or the operator are left untouched; we layer over them. `var`
// rather than `const` so tests can redirect to a temp directory.
var resolvedDropInPath = "/etc/systemd/resolved.conf.d/jabali.conf"

// resolvConfPath is the host's active resolver file. Read for the "current
// system" fallback when our drop-in doesn't exist, so the panel shows the
// admin what DNS the OS is actually using rather than an empty form. `var`
// for test redirection.
var resolvConfPath = "/etc/resolv.conf"

type systemResolverGetResponse struct {
	Active       bool     `json:"active"`
	Resolvers    []string `json:"resolvers"`
	SearchDomain string   `json:"search_domain"`
	// Source is one of: "drop-in" (panel-managed file present),
	// "system" (read from /etc/resolv.conf), "none" (neither readable).
	Source string `json:"source"`
}

// systemResolverGetHandler reports the host's current DNS configuration.
// The panel-owned drop-in is preferred; absent that, we parse
// /etc/resolv.conf so the admin sees what the OS is actually resolving
// against. The installer does not touch DNS — this endpoint is read-only
// for the "what's in place right now" view.
func systemResolverGetHandler(_ context.Context, _ json.RawMessage) (any, error) {
	active := systemdResolvedActive()
	resolvers, search, source, err := readResolverDropIn(resolvedDropInPath)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read drop-in: %v", err)}
	}
	if source == "none" {
		// Fall back to /etc/resolv.conf so the panel can show the admin
		// the resolvers the host is actually using.
		sysResolvers, sysSearch, found, rerr := readResolvConf(resolvConfPath)
		if rerr == nil && found {
			resolvers = sysResolvers
			search = sysSearch
			source = "system"
		}
	}
	return systemResolverGetResponse{
		Active:       active,
		Resolvers:    resolvers,
		SearchDomain: search,
		Source:       source,
	}, nil
}

// systemdResolvedActive reports whether systemd-resolved.service is active.
// We only call systemctl once per handler — the result informs the UI but
// doesn't gate reads of the drop-in file (which may exist even when the
// service is stopped).
var systemdResolvedActive = func() bool {
	out, _ := runSystemctl(context.Background(), "is-active", "systemd-resolved.service")
	return strings.TrimSpace(string(out)) == "active"
}

// readResolverDropIn parses the panel-owned drop-in at path. Absent file is
// not an error — returns empty slices with source="none". Malformed lines
// are skipped rather than erroring; systemd-resolved itself is lenient, we
// match its behavior.
var readResolverDropIn = func(path string) (resolvers []string, search, source string, err error) {
	f, err := os.Open(path) // #nosec G304 — path is a fixed constant
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", "none", nil
		}
		return nil, "", "", err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "DNS":
			for _, tok := range strings.Fields(value) {
				if _, perr := netip.ParseAddr(tok); perr == nil {
					resolvers = append(resolvers, tok)
				}
			}
		case "Domains":
			fields := strings.Fields(value)
			if len(fields) > 0 {
				search = fields[0]
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", "", err
	}
	return resolvers, search, "drop-in", nil
}

// readResolvConf parses /etc/resolv.conf for `nameserver` and `search`/
// `domain` directives. Used as the fallback when the panel's drop-in
// hasn't been written — so the admin sees current host DNS in the UI.
// `found` is false when the file is missing or contains no nameservers,
// signalling the caller to report source="none" instead of "system".
var readResolvConf = func(path string) (resolvers []string, search string, found bool, err error) {
	f, err := os.Open(path) // #nosec G304 — fixed path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "nameserver":
			if _, perr := netip.ParseAddr(fields[1]); perr == nil {
				resolvers = append(resolvers, fields[1])
			}
		case "search", "domain":
			if search == "" {
				search = fields[1]
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", false, err
	}
	return resolvers, search, len(resolvers) > 0, nil
}

func init() {
	Default.Register("system.resolver.get", systemResolverGetHandler)
}
