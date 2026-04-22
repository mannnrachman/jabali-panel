package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainEmailDisableParams is the request shape for domain.email_disable.
//
// Called when the panel flips domains.email_enabled from 1 -> 0. Panel
// is the source of truth (ADR-0042 / ADR-0045): Stalwart's SqlDirectory
// re-reads jabali_panel.mailboxes on every auth, and once email_enabled
// dips to 0 the panel-side JOIN blocks new auths immediately. Orphan
// records in Stalwart's registry (Account / DkimSignature / Domain)
// are harmless — they can never be authed against while the panel
// row is off.
//
// What this handler does NOT do (intentionally):
//
//  1. Destroy the Stalwart registry Domain. v0.16 responds
//     `objectIsLinked` when Accounts or DkimSignatures reference it,
//     and cleaning those up first would cascade across every mailbox —
//     slow and error-prone. Leaving them stands up a cleaner re-enable
//     path too (Stalwart already has the right state, no re-sync).
//
//  2. Remove the DKIM private key from /etc/jabali-panel/dkim/. ADR-0043
//     keeps the key so re-enable doesn't re-roll and invalidate cached
//     DKIM signatures at downstream receivers. Removing it would
//     contradict the panel DB holding the public key side.
//
// What it still does:
//
//   - Reload Stalwart. The SqlDirectory re-checks the email_enabled
//     column on every auth anyway, so this is belt-and-braces — it
//     drops any in-memory auth state immediately instead of waiting
//     for the next attempt.
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

	// Reload Stalwart so any in-memory view of this domain's auth
	// drops. A SIGHUP-equivalent reload is enough; we don't need to
	// stop any unit (other domains may still have email enabled).
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
