package eventsources

import "testing"

func TestParseAcceptedLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		msg         string
		wantUser    string
		wantIP      string
		wantMethod  string
	}{
		{
			name:       "publickey",
			msg:        "Accepted publickey for alice from 10.0.0.5 port 49152 ssh2: ED25519 SHA256:abc",
			wantUser:   "alice",
			wantIP:     "10.0.0.5",
			wantMethod: "publickey",
		},
		{
			name:       "password",
			msg:        "Accepted password for bob from 192.0.2.1 port 51010 ssh2",
			wantUser:   "bob",
			wantIP:     "192.0.2.1",
			wantMethod: "password",
		},
		{
			name: "rejects failed",
			msg:  "Failed password for invalid user root from 1.2.3.4 port 12345 ssh2",
		},
		{
			name: "rejects malformed",
			msg:  "Accepted publickey",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			user, ip, method := parseAcceptedLine(tc.msg)
			if user != tc.wantUser || ip != tc.wantIP || method != tc.wantMethod {
				t.Fatalf("got (%q,%q,%q), want (%q,%q,%q)",
					user, ip, method, tc.wantUser, tc.wantIP, tc.wantMethod)
			}
		})
	}
}
