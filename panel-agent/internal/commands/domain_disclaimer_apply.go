package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domainDisclaimerApplyParams is the panel → agent request.
//
// Upserts a system sieve script jabali-disclaimer-<domain_name> that
// appends the disclaimer text to matching outbound mail body parts.
// When Enabled=false, destroys any existing script.
//
// Body-rewrite on HTML parts is a known Stalwart sieve limitation
// (ADR-0052). First implementation: text/plain only via `body` +
// `replace` + `foreverypart`. HTML coverage deferred to M6.6 behind
// Spike A/B verification on live VM.
type domainDisclaimerApplyParams struct {
	DomainName string `json:"domain_name"`
	Enabled    bool   `json:"enabled"`
	Text       string `json:"text"`
}

type domainDisclaimerApplyResponse struct {
	Ok bool `json:"ok"`
}

func domainDisclaimerApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p domainDisclaimerApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}
	scriptName := "jabali-disclaimer-" + sanitizeScriptName(p.DomainName)

	if !p.Enabled || strings.TrimSpace(p.Text) == "" {
		// Tear down.
		args := map[string]any{"destroy": []string{scriptName}}
		var result jmapSetResult
		_ = jmapCall(ctx, "x:SieveSystemScript/set", args, &result) // best-effort
		return domainDisclaimerApplyResponse{Ok: true}, nil
	}

	body := renderDisclaimerSieve(p.DomainName, p.Text)
	// Try create first, fall back to update if already present.
	createArgs := map[string]any{
		"create": map[string]any{
			scriptName: map[string]any{
				"name":     scriptName,
				"isActive": true,
				"contents": body,
			},
		},
	}
	var result jmapSetResult
	if err := jmapCall(ctx, "x:SieveSystemScript/set", createArgs, &result); err != nil {
		return nil, err
	}
	if _, exists := result.NotCreated[scriptName]; exists {
		updateArgs := map[string]any{
			"update": map[string]any{
				scriptName: map[string]any{
					"contents": body,
					"isActive": true,
				},
			},
		}
		var r2 jmapSetResult
		if err := jmapCall(ctx, "x:SieveSystemScript/set", updateArgs, &r2); err != nil {
			return nil, fmt.Errorf("disclaimer sieve update: %w", err)
		}
		if reason, ok := r2.NotUpdated[scriptName]; ok {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("disclaimer sieve update refused: %s", string(reason))}
		}
	}
	return domainDisclaimerApplyResponse{Ok: true}, nil
}

func renderDisclaimerSieve(domain, text string) string {
	// Escape double-quotes in text.
	esc := strings.ReplaceAll(text, `"`, `\"`)
	var b strings.Builder
	b.WriteString(`require ["envelope","body","replace","foreverypart"];` + "\n")
	fmt.Fprintf(&b, "# jabali-managed disclaimer for %s — do not edit; overwritten by reconciler\n", domain)
	fmt.Fprintf(&b, `if envelope :domain "from" "%s" {`+"\n", domain)
	b.WriteString("  foreverypart {\n")
	b.WriteString(`    if header :mime :contenttype :is "content-type" "text/plain" {` + "\n")
	fmt.Fprintf(&b, `      replace text:\n${ORIGINAL_BODY}\n\n-- \n%s\n.\n;`+"\n", esc)
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

func sanitizeScriptName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

func init() {
	Default.Register("domain.disclaimer_apply", domainDisclaimerApplyHandler)
}
