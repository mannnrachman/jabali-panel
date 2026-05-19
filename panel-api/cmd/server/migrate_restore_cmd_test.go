package main

import "testing"

func TestCpmoveSourceUser(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"cpmove-newpuzzleans.tar.gz", "newpuzzleans"},
		{"/home/u/domains/x/public_html/cpmove-newpuzzleans.tar.gz", "newpuzzleans"},
		{"cpmove-bob123.tar.gz", "bob123"},
		{"cpmove-user_with-dots.v2.tar.gz", "user_with-dots.v2"},
		{"backup-1.2.3_bob.tar.gz", ""},  // pkgacct shape — not auto-derived
		{"cpmove-bob.tar", ""},           // not .tar.gz
		{"random.tar.gz", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := cpmoveSourceUser(c.path); got != c.want {
			t.Errorf("cpmoveSourceUser(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
