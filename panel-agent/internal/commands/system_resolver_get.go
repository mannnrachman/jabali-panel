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

type systemResolverGetResponse struct {
	Active        bool     `json:"active"`
	Resolvers     []string `json:"resolvers"`
	SearchDomain  string   `json:"search_domain"`
	Source        string   `json:"source"` // "drop-in" | "system" | "none"
}

func systemResolverGetHandler(_ context.Context, _ json.RawMessage) (any, error) {
	active := systemdResolvedActive()
	resolvers, search, source, err := readResolverDropIn(resolvedDropInPath)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("read drop-in: %v", err)}
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

func init() {
	Default.Register("system.resolver.get", systemResolverGetHandler)
}
