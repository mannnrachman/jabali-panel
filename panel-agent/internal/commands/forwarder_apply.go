package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// forwarderApplyParams is the panel → agent request for forwarder.apply.
//
// Idempotent apply of a mailbox's forwarders. Caller sends the full desired
// state; agent writes x:UserAccount.aliases for type=alias entries and a
// single concatenated x:SieveUserScript per mailbox for type=external
// entries (Stalwart allows only one active sieve script per account —
// schema SieveScript.isActive).
type forwarderApplyParams struct {
	MailboxEmail string             `json:"mailbox_email"`
	Aliases      []forwarderAlias   `json:"aliases"`   // local parts within the mailbox's own domain
	Externals    []string           `json:"externals"` // target emails
}

type forwarderAlias struct {
	LocalPart string `json:"local_part"`
	// DomainID is the Stalwart x:Domain id the alias belongs to. Resolved
	// agent-side from the email's domain.
}

type forwarderApplyResponse struct {
	Ok bool `json:"ok"`
}

func forwarderApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p forwarderApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if _, err := requireEmail(p.MailboxEmail); err != nil {
		return nil, err
	}

	acctID, err := accountIDByEmail(ctx, p.MailboxEmail)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("resolve account: %v", err)}
	}
	if acctID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeNotFound, Message: "mailbox not yet registered"}
	}

	// Resolve the account's domain id.
	at := strings.LastIndex(p.MailboxEmail, "@")
	domainName := p.MailboxEmail[at+1:]
	domainID, err := domainIDByName(ctx, domainName)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("resolve domain: %v", err)}
	}
	if domainID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeNotFound, Message: "domain not in Stalwart registry"}
	}

	// 1. Replace account aliases.
	if err := applyAccountAliases(ctx, acctID, domainID, p.Aliases); err != nil {
		return nil, err
	}

	// 2. Replace the concatenated sieve script for external forwards.
	if err := applyExternalSieve(ctx, acctID, p.Externals); err != nil {
		return nil, err
	}

	return forwarderApplyResponse{Ok: true}, nil
}

func applyAccountAliases(ctx context.Context, acctID, domainID string, aliases []forwarderAlias) error {
	entries := make([]map[string]any, 0, len(aliases))
	for _, a := range aliases {
		if a.LocalPart == "" {
			continue
		}
		entries = append(entries, map[string]any{
			"name":     a.LocalPart,
			"domainId": domainID,
			"enabled":  true,
		})
	}
	args := map[string]any{
		"update": map[string]any{
			acctID: map[string]any{
				"aliases": entries,
			},
		},
	}
	var result jmapSetResult
	if err := jmapCall(ctx, "x:Account/User/set", args, &result); err != nil {
		return err
	}
	if reason, ok := result.NotUpdated[acctID]; ok {
		return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("Account alias set refused: %s", string(reason))}
	}
	return nil
}

func applyExternalSieve(ctx context.Context, acctID string, externals []string) error {
	scriptName := "jabali-fwds"
	if len(externals) == 0 {
		// Destroy the script if it exists.
		args := map[string]any{
			"accountId": acctID,
			"destroy":   []string{scriptName},
		}
		var result jmapSetResult
		_ = jmapCall(ctx, "SieveScript/set", args, &result) // best-effort — may not exist
		return nil
	}
	var body strings.Builder
	body.WriteString(`require ["copy"];` + "\n")
	for _, target := range externals {
		if target == "" {
			continue
		}
		fmt.Fprintf(&body, "redirect :copy %q;\n", target)
	}
	// Upsert + activate.
	args := map[string]any{
		"accountId": acctID,
		"create": map[string]any{
			scriptName: map[string]any{
				"name":     scriptName,
				"isActive": true,
				"contents": body.String(),
			},
		},
	}
	var result jmapSetResult
	if err := jmapCall(ctx, "x:SieveUserScript/set", args, &result); err != nil {
		return err
	}
	if reason, ok := result.NotCreated[scriptName]; ok {
		// Probably already exists → update instead.
		args = map[string]any{
			"accountId": acctID,
			"update": map[string]any{
				scriptName: map[string]any{
					"contents": body.String(),
					"isActive": true,
				},
			},
		}
		var result2 jmapSetResult
		if err := jmapCall(ctx, "x:SieveUserScript/set", args, &result2); err != nil {
			return fmt.Errorf("sieve upsert: %w (first NotCreated: %s)", err, string(reason))
		}
		if reason2, ok2 := result2.NotUpdated[scriptName]; ok2 {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("sieve update refused: %s", string(reason2))}
		}
	}
	return nil
}

func init() {
	Default.Register("forwarder.apply", forwarderApplyHandler)
}
