package reconciler

import (
	"encoding/json"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// TestThrottlePayloadFor_PerUserEmitsSenderFilter pins that a user-
// scope row with an email scope_ref renders into a Stalwart payload
// whose match expression filters by that exact sender. Without this
// the throttle would silently apply to every sender (the v1 always-
// fire behaviour — wrong + dangerous, an admin THINKS one mailbox is
// capped but everyone hits the bucket).
func TestThrottlePayloadFor_PerUserEmitsSenderFilter(t *testing.T) {
	addr := "user@example.com"
	row := &models.MailOutboundPolicy{
		Scope: models.OutboundScopeUser, ScopeRef: &addr, MaxPerHour: 50, Enabled: true,
	}
	p := throttlePayloadFor(row)
	body, _ := json.Marshal(p.Match)
	if !strings.Contains(string(body), "sender == 'user@example.com'") {
		t.Errorf("user-scope payload missing sender filter\nmatch=%s", body)
	}
	if !p.Key["sender"] {
		t.Errorf("user-scope key should include sender, got %v", p.Key)
	}
}

func TestThrottlePayloadFor_PerDomainEmitsSenderDomainFilter(t *testing.T) {
	dom := "example.com"
	row := &models.MailOutboundPolicy{
		Scope: models.OutboundScopeDomain, ScopeRef: &dom, MaxPerHour: 500, Enabled: true,
	}
	p := throttlePayloadFor(row)
	body, _ := json.Marshal(p.Match)
	if !strings.Contains(string(body), "sender_domain == 'example.com'") {
		t.Errorf("domain-scope payload missing sender_domain filter\nmatch=%s", body)
	}
	if !p.Key["senderDomain"] {
		t.Errorf("domain-scope key should include senderDomain, got %v", p.Key)
	}
}

// Global scope keeps the always-fire match — backwards-compat with v1.
func TestThrottlePayloadFor_GlobalKeepsAlwaysFire(t *testing.T) {
	row := &models.MailOutboundPolicy{
		Scope: models.OutboundScopeGlobal, MaxPerHour: 10000, Enabled: true,
	}
	p := throttlePayloadFor(row)
	body, _ := json.Marshal(p.Match)
	if string(body) != `{"match":{},"else":"true"}` {
		t.Errorf("global-scope match should be always-fire, got %s", body)
	}
	if len(p.Key) != 0 {
		t.Errorf("global-scope key map should be empty, got %v", p.Key)
	}
}

// User scope with empty scope_ref falls back to always-fire so a
// half-configured row doesn't crash the reconciler (and the API
// handler rejects empty scope_ref on POST, so this only fires on
// legacy rows from before the validator landed).
func TestThrottlePayloadFor_UserScopeWithoutRefFallsBackToAlwaysFire(t *testing.T) {
	row := &models.MailOutboundPolicy{
		Scope: models.OutboundScopeUser, ScopeRef: nil, MaxPerHour: 10, Enabled: true,
	}
	p := throttlePayloadFor(row)
	body, _ := json.Marshal(p.Match)
	if string(body) != `{"match":{},"else":"true"}` {
		t.Errorf("user scope without scope_ref should fall back to always-fire, got %s", body)
	}
}
