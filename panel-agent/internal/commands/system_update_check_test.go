package commands

import "testing"

func TestParseIntSafe(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"3", 3},
		{"42", 42},
		{"", 0},
		{"abc", 0},
		{"3a", 0},
		{"  3  ", 0}, // not stripped — caller TrimSpace's
	}
	for _, c := range cases {
		if got := parseIntSafe(c.in); got != c.want {
			t.Errorf("parseIntSafe(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}
