package models

import "testing"

// AutomationScopes.Has() is the gate every M44 automation route runs
// past. Bug here = privilege escalation, so the matrix is exhaustive.
func TestAutomationScopes_Has(t *testing.T) {
	cases := []struct {
		name    string
		granted AutomationScopes
		want    string
		expect  bool
	}{
		{"exact match", AutomationScopes{"read:domains"}, "read:domains", true},
		{"exact mismatch", AutomationScopes{"read:domains"}, "read:users", false},
		{"wildcard match domains", AutomationScopes{"read:*"}, "read:domains", true},
		{"wildcard match users", AutomationScopes{"read:*"}, "read:users", true},
		{"wildcard match status", AutomationScopes{"read:*"}, "read:status", true},
		{"wildcard does not cross category", AutomationScopes{"read:*"}, "write:domains", false},
		{"empty scopes never match", AutomationScopes{}, "read:domains", false},
		{"nil scopes never match", nil, "read:domains", false},
		{"multiple grants pick correct one", AutomationScopes{"read:domains", "read:users"}, "read:users", true},
		{"typo wildcard does not over-grant",
			AutomationScopes{"rea:*"}, "read:domains", false},
		{"prefix-but-not-wildcard does not over-grant",
			AutomationScopes{"read"}, "read:domains", false},
		{"colon-only scope is not a wildcard",
			AutomationScopes{":*"}, "read:domains", false},
		{"write wildcard does not match read scope",
			AutomationScopes{"write:*"}, "read:domains", false},
		{"empty target string never matches",
			AutomationScopes{"read:*"}, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.granted.Has(c.want)
			if got != c.expect {
				t.Fatalf("Has(%q) on %v: got %v, want %v", c.want, c.granted, got, c.expect)
			}
		})
	}
}
