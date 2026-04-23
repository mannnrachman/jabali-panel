package main

import "testing"

func TestHostnameIsMailRoutable(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		// RFC 6761 reserved — must be rejected.
		{"jabali-panel.local", false},
		{"example.local", false},
		{"panel.localhost", false},
		{"foo.invalid", false},
		{"bar.test", false},
		{"baz.example", false},
		// Case + trailing dot normalization.
		{"Panel.LOCAL", false},
		{"panel.local.", false},
		// Routable.
		{"mail.example.com", true},
		{"panel.linux-hosting.co.il", true},
		{"host.org", true},
		{"a.b.c.d.net", true},
		// Edge cases.
		{"", false},
		{".", false},
	}
	for _, tc := range cases {
		if got := hostnameIsMailRoutable(tc.host); got != tc.want {
			t.Errorf("hostnameIsMailRoutable(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
