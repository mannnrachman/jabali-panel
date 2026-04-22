// Package commands — pdns.recursor_{add_zone,remove_zone,list}.
//
// These three handlers share a single *pdnsrecursor.Manager instance via
// getRecursorMgr() so the package-local mutex and rolling .bak state is
// consistent across commands. The singleton is lazy-initialised; tests
// replace it via setRecursorMgrForTest.
//
// See ADR-0047 for design rationale and plans/m6.3-pdns-recursor.md §Step 4
// for the wire-contract spec.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdnsrecursor"
)

const defaultForwardPort = 5300

var (
	recursorMgr     *pdnsrecursor.Manager
	recursorMgrOnce sync.Once
	recursorMgrErr  error
)

// getRecursorMgr returns the lazy-initialised package singleton. First call
// constructs with production defaults; subsequent calls reuse the same
// instance. Tests can override via setRecursorMgrForTest.
func getRecursorMgr() (*pdnsrecursor.Manager, error) {
	recursorMgrOnce.Do(func() {
		recursorMgr, recursorMgrErr = pdnsrecursor.New(pdnsrecursor.Options{})
	})
	return recursorMgr, recursorMgrErr
}

// setRecursorMgrForTest replaces the singleton. Not for production use.
func setRecursorMgrForTest(m *pdnsrecursor.Manager) {
	recursorMgr = m
	recursorMgrOnce.Do(func() {}) // mark as initialised
}

// resetRecursorMgrForTest wipes the singleton so the next getRecursorMgr
// call re-initialises. Used by tests that want to exercise the Once path.
func resetRecursorMgrForTest() {
	recursorMgr = nil
	recursorMgrErr = nil
	recursorMgrOnce = sync.Once{}
}

// --- pdns.recursor_add_zone ---

type pdnsRecursorAddZoneParams struct {
	Zone string `json:"zone"`
	Addr string `json:"addr"`
	Port int    `json:"port,omitempty"` // default defaultForwardPort; handler fills
}

type pdnsRecursorAddZoneResponse struct {
	Zone    string `json:"zone"`
	Changed bool   `json:"changed"`
}

func pdnsRecursorAddZoneHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p pdnsRecursorAddZoneParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if p.Zone == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "zone required"}
	}
	if p.Addr == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "addr required"}
	}
	// Default port resolved HERE, not inside Manager.AddZone (see Step 4
	// plan notes — strict Manager keeps the handler + CLI from diverging).
	if p.Port == 0 {
		p.Port = defaultForwardPort
	}
	mgr, err := getRecursorMgr()
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("recursor mgr init: %v", err)}
	}
	changed, err := mgr.AddZone(ctx, pdnsrecursor.Entry{Zone: p.Zone, Addr: p.Addr, Port: p.Port})
	if err != nil {
		// Validator errors are invalid_argument; reload/probe failures are
		// internal. We don't have structured error kinds from the package
		// yet — classify crudely by substring for now.
		code := agentwire.CodeInternal
		msg := err.Error()
		if looksLikeValidation(msg) {
			code = agentwire.CodeInvalidArgument
		}
		return nil, &agentwire.AgentError{Code: code, Message: msg}
	}
	return pdnsRecursorAddZoneResponse{Zone: p.Zone, Changed: changed}, nil
}

// --- pdns.recursor_remove_zone ---

type pdnsRecursorRemoveZoneParams struct {
	Zone string `json:"zone"`
}

type pdnsRecursorRemoveZoneResponse struct {
	Zone    string `json:"zone"`
	Changed bool   `json:"changed"`
}

func pdnsRecursorRemoveZoneHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p pdnsRecursorRemoveZoneParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	if p.Zone == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "zone required"}
	}
	mgr, err := getRecursorMgr()
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("recursor mgr init: %v", err)}
	}
	changed, err := mgr.RemoveZone(ctx, p.Zone)
	if err != nil {
		code := agentwire.CodeInternal
		msg := err.Error()
		if looksLikeValidation(msg) {
			code = agentwire.CodeInvalidArgument
		}
		return nil, &agentwire.AgentError{Code: code, Message: msg}
	}
	return pdnsRecursorRemoveZoneResponse{Zone: p.Zone, Changed: changed}, nil
}

// --- pdns.recursor_list ---

type pdnsRecursorListEntry struct {
	Zone string `json:"zone"`
	Addr string `json:"addr"`
	Port int    `json:"port"`
}

type pdnsRecursorListResponse struct {
	Entries []pdnsRecursorListEntry `json:"entries"`
}

func pdnsRecursorListHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	mgr, err := getRecursorMgr()
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("recursor mgr init: %v", err)}
	}
	entries, err := mgr.List(ctx)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	out := pdnsRecursorListResponse{Entries: make([]pdnsRecursorListEntry, len(entries))}
	for i, e := range entries {
		out.Entries[i] = pdnsRecursorListEntry{Zone: e.Zone, Addr: e.Addr, Port: e.Port}
	}
	return out, nil
}

// looksLikeValidation heuristic — Manager wraps its errors as
// "add_zone: zone X: ..." where the inner pieces are validator messages.
// We classify crudely; a structured error kind can replace this later.
func looksLikeValidation(msg string) bool {
	// Cheap substring match on known validator-error shapes.
	for _, needle := range []string{
		"is not a valid DNS name",
		"has trailing dot",
		"has uppercase",
		"port",
		"addr",
		"looks like an IP",
		"self-loop",
		"is not loopback",
		"zone is empty",
	} {
		if containsFold(msg, needle) {
			return true
		}
	}
	return false
}

// containsFold — tiny case-insensitive substring check so we don't pull
// strings.EqualFold into the import list just for this.
func containsFold(s, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(s); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			c1, c2 := s[i+j], needle[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 'a' - 'A'
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 'a' - 'A'
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func init() {
	Default.Register("pdns.recursor_add_zone", pdnsRecursorAddZoneHandler)
	Default.Register("pdns.recursor_remove_zone", pdnsRecursorRemoveZoneHandler)
	Default.Register("pdns.recursor_list", pdnsRecursorListHandler)
}
