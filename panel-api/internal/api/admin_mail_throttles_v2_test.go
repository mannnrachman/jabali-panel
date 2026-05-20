package api

import (
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// Pin that scopeRefEmailRe rejects every shape Stalwart Expression
// injection needs (quotes, backslashes, control chars, embedded
// expression operators). The reconciler embeds scope_ref verbatim
// into `sender == '<ref>'` — a quote escapes the literal and the
// throttle silently degrades. This is a real security boundary, not
// a UX nicety.
func TestValidateScopeRef_RejectsInjectionShapes(t *testing.T) {
	bad := []struct{ scope, ref string }{
		{models.OutboundScopeUser, ""},
		{models.OutboundScopeUser, "no-at-sign.example.com"},
		{models.OutboundScopeUser, "spaces in@email.com"},
		{models.OutboundScopeUser, "quote'inject@example.com"},
		{models.OutboundScopeUser, "backslash\\inject@example.com"},
		{models.OutboundScopeUser, "x@" + "evil' || 1==1 || sender == 'a"},
		{models.OutboundScopeDomain, "spaces in.com"},
		{models.OutboundScopeDomain, "quote'.com"},
		{models.OutboundScopeDomain, "backslash\\.com"},
		{models.OutboundScopeDomain, "UPPERCASE.COM"},
		{models.OutboundScopeDomain, ".leading-dot"},
		{models.OutboundScopeGlobal, "anything"}, // global doesn't take a ref
	}
	for _, c := range bad {
		if validateScopeRef(c.scope, c.ref) {
			t.Errorf("scope=%s ref=%q should be rejected", c.scope, c.ref)
		}
	}
}

func TestValidateScopeRef_AcceptsValid(t *testing.T) {
	good := []struct{ scope, ref string }{
		{models.OutboundScopeUser, "user@example.com"},
		{models.OutboundScopeUser, "first.last+tag@sub.example.co.il"},
		{models.OutboundScopeDomain, "example.com"},
		{models.OutboundScopeDomain, "deep.sub.example.co.il"},
	}
	for _, c := range good {
		if !validateScopeRef(c.scope, c.ref) {
			t.Errorf("scope=%s ref=%q should be accepted", c.scope, c.ref)
		}
	}
}
