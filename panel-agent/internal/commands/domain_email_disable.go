package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainEmailDisableParams is the request shape for domain.email_disable.
//
// Called when the panel flips domains.email_enabled from 1 -> 0. The
// agent (v0.16 flow — ADR-0045):
//  1. Resolves the Stalwart registry Domain id via JMAP Domain/query.
//  2. If present, JMAP Domain/set destroy. Stalwart's garbage collection
//     will lazily drop the associated DkimSignature + orphan Account
//     records; we don't chase those eagerly. notFound at destroy time is
//     fine — the domain may have never been synced into the registry
//     (no prior email enable on this specific Stalwart install).
//  3. Removes the DKIM private key file. Idempotent: missing file OK.
//  4. `systemctl reload jabali-stalwart` so any in-memory auth state for
//     this domain's mailboxes drops cleanly. We do NOT stop the unit —
//     other domains may still have email enabled (ADR-0045).
type domainEmailDisableParams struct {
	DomainID   string `json:"domain_id"`
	DomainName string `json:"domain_name"`
}

func domainEmailDisableHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p domainEmailDisableParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_id required"}
	}
	if err := validateDomainNameForShell(p.DomainName); err != nil {
		return nil, err
	}

	// Drop the registry Domain record. Best-effort JMAP call: if
	// Stalwart isn't reachable (loopback wedge), surface CodeUnavailable
	// so the panel retries — the panel's reconciler will re-queue this
	// on the next convergence tick.
	domainID, err := domainIDByName(ctx, p.DomainName)
	if err != nil {
		return nil, err
	}
	if domainID != "" {
		if err := domainDestroy(ctx, domainID); err != nil {
			return nil, err
		}
	}

	// Remove the DKIM private key. Idempotent: missing file is fine
	// (re-disable after a manual cleanup shouldn't error).
	keyPath := filepath.Join(dkimKeyDirFunc(), p.DomainName+".key")
	if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("remove DKIM key at %s: %v", keyPath, err),
		}
	}

	// Reload Stalwart so any in-memory view of this domain's auth
	// drops. A SIGHUP-equivalent reload is enough; we don't need to
	// stop any unit.
	if out, err := runSystemctl(ctx, "reload", "jabali-stalwart.service"); err != nil {
		if !isReloadNotSupportedErr(out) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("systemctl reload jabali-stalwart: %v (%s)", err, bytesTrim(out)),
			}
		}
	}
	return okBody{Ok: true}, nil
}

func init() {
	Default.Register("domain.email_disable", domainEmailDisableHandler)
}
