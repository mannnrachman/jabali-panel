package models

import "testing"

func TestDeriveFamily(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Plain IPv4.
		{"1.2.3.4", "ipv4"},
		{"203.0.113.42", "ipv4"},
		{"0.0.0.0", "ipv4"},
		{"255.255.255.255", "ipv4"},

		// Plain IPv6.
		{"::1", "ipv6"},
		{"2001:db8::1", "ipv6"},
		{"fe80::1", "ipv6"},
		{"::", "ipv6"},

		// IPv4-mapped IPv6 keeps the v6 family because the operator
		// pasted v6 syntax — DeriveFamily checks for ":" before
		// asking To4.
		{"::ffff:1.2.3.4", "ipv6"},

		// Garbage and edge cases.
		{"", ""},
		{"not-an-ip", ""},
		{"999.999.999.999", ""},
		{"1.2.3", ""},
		{"2001:db8:::1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := DeriveFamily(tc.in)
			if got != tc.want {
				t.Errorf("DeriveFamily(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestManagedIP_TableName pins the table name so a refactor of the
// model package can't silently rename the migration target.
func TestManagedIP_TableName(t *testing.T) {
	if (ManagedIP{}).TableName() != "managed_ips" {
		t.Errorf("ManagedIP.TableName() = %q, want %q",
			(ManagedIP{}).TableName(), "managed_ips")
	}
}
