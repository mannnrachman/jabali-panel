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
		// Tear down: destroy takes server-assigned ids, not names, so look
		// up the existing id first. Best-effort (absent → nothing to do).
		existingID, _ := findSieveSystemScriptID(ctx, scriptName)
		if existingID != "" {
			args := map[string]any{"destroy": []string{existingID}}
			var result jmapSetResult
			_ = jmapCall(ctx, "x:SieveSystemScript/set", args, &result)
		}
		return domainDisclaimerApplyResponse{Ok: true}, nil
	}

	body := renderDisclaimerSieve(p.DomainName, p.Text)

	// Resolve existing script id by name so update/create is dispatched correctly.
	// Stalwart `Set` uses server-assigned ids for `update` keys; using the
	// script's name as key silently no-ops (no `updated`, no `notUpdated`).
	existingID, err := findSieveSystemScriptID(ctx, scriptName)
	if err != nil {
		return nil, err
	}

	var result jmapSetResult
	if existingID == "" {
		createArgs := map[string]any{
			"create": map[string]any{
				scriptName: map[string]any{
					"name":     scriptName,
					"isActive": true,
					"contents": body,
				},
			},
		}
		if err := jmapCall(ctx, "x:SieveSystemScript/set", createArgs, &result); err != nil {
			return nil, err
		}
		if reason, bad := result.NotCreated[scriptName]; bad {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("disclaimer sieve create refused: %s", string(reason)),
			}
		}
	} else {
		updateArgs := map[string]any{
			"update": map[string]any{
				existingID: map[string]any{
					"contents": body,
					"isActive": true,
				},
			},
		}
		if err := jmapCall(ctx, "x:SieveSystemScript/set", updateArgs, &result); err != nil {
			return nil, fmt.Errorf("disclaimer sieve update: %w", err)
		}
		if reason, bad := result.NotUpdated[existingID]; bad {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("disclaimer sieve update refused: %s", string(reason)),
			}
		}
	}
	return domainDisclaimerApplyResponse{Ok: true}, nil
}

// findSieveSystemScriptID returns the Stalwart-assigned id for a system sieve
// script matching `name` exactly, or "" if none exists.
func findSieveSystemScriptID(ctx context.Context, name string) (string, error) {
	args := map[string]any{"filter": map[string]any{"name": name}}
	var qr jmapQueryResult
	if err := jmapCall(ctx, "x:SieveSystemScript/query", args, &qr); err != nil {
		return "", err
	}
	if len(qr.IDs) == 0 {
		return "", nil
	}
	return qr.IDs[0], nil
}

// renderDisclaimerSieve builds a sieve that appends `text` to outbound mail
// from `domain`. It uses RFC 5173 (body), RFC 5229 (variables), RFC 5173
// foreverypart + RFC 5703 mime/extracttext/replace to capture each part's
// body and rewrite it with the disclaimer appended. Covers both text/plain
// and text/html parts.
//
// Spike A verified on Stalwart v1.0.0 (2026-04-23): required extensions
// compile, and per-part replace with an extracted body variable works for
// both MIME types.
func renderDisclaimerSieve(domain, text string) string {
	plainText := sieveEscape(text)
	htmlText := sieveEscape(htmlEscape(text))
	var b strings.Builder
	b.WriteString(`require ["envelope","variables","mime","foreverypart","extracttext","replace"];` + "\n")
	fmt.Fprintf(&b, "# jabali-managed disclaimer for %s — do not edit; overwritten by reconciler\n", domain)
	fmt.Fprintf(&b, "if envelope :domain \"from\" \"%s\" {\n", domain)
	b.WriteString("  foreverypart {\n")
	b.WriteString("    if header :mime :contenttype :is \"Content-Type\" \"text/plain\" {\n")
	b.WriteString("      if extracttext \"jabali_orig\" {\n")
	fmt.Fprintf(&b, "        replace \"${jabali_orig}\\n\\n-- \\n%s\\n\";\n", plainText)
	b.WriteString("      }\n")
	b.WriteString("    } elsif header :mime :contenttype :is \"Content-Type\" \"text/html\" {\n")
	b.WriteString("      if extracttext \"jabali_orig\" {\n")
	fmt.Fprintf(&b, "        replace \"${jabali_orig}<hr><p>%s</p>\";\n", htmlText)
	b.WriteString("      }\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// sieveEscape escapes a string for inclusion inside a sieve double-quoted
// string literal: backslash first, then double-quote, then newline (as the
// literal \n escape). Sieve strings do not process \t or other escapes.
func sieveEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// htmlEscape escapes HTML-special characters so operator-typed text can't
// inject markup into the HTML-part disclaimer.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, `&`, `&amp;`)
	s = strings.ReplaceAll(s, `<`, `&lt;`)
	s = strings.ReplaceAll(s, `>`, `&gt;`)
	s = strings.ReplaceAll(s, `"`, `&quot;`)
	return s
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
