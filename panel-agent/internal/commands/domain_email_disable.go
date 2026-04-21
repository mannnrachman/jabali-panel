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
// Called when the panel flips domains.email_enabled from 1 -> 0. The agent:
//  1. Removes the DKIM key file (/etc/jabali-panel/dkim/<domain>.key).
//     The DNS record deletion is the panel reconciler's job (Step 5).
//  2. Reloads Stalwart so it re-reads the SQL directory and stops
//     accepting auth for this domain's mailboxes.
//
// Notable non-action: we do NOT stop jabali-stalwart or jabali-webmail.
// Other domains may still have email enabled. Unit shutdown is a manual
// operator decision, not automatic on last-domain-disable.
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

	// Remove the DKIM private key. Idempotent: missing file is fine
	// (re-disable after a manual cleanup shouldn't error).
	keyPath := filepath.Join(dkimKeyDirFunc(), p.DomainName+".key")
	if err := os.Remove(keyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("remove DKIM key at %s: %v", keyPath, err),
		}
	}

	// Reload Stalwart so the SQL directory sees email_enabled = 0 for
	// this domain. A SIGHUP-equivalent reload is enough; we don't need
	// to stop any unit.
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
