package eventsources

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestReverseDNSBLQuery(t *testing.T) {
	cases := []struct {
		ip, rbl, want string
	}{
		{"1.2.3.4", "zen.spamhaus.org", "4.3.2.1.zen.spamhaus.org"},
		{"182.54.236.60", "bl.spamcop.net", "60.236.54.182.bl.spamcop.net"},
		{"not-an-ip", "x.y", ""},
		{"::1", "x.y", ""}, // IPv6 — handled separately by isIPv4 gate
	}
	for _, c := range cases {
		if got := reverseDNSBLQuery(c.ip, c.rbl); got != c.want {
			t.Errorf("reverseDNSBLQuery(%q, %q) = %q, want %q", c.ip, c.rbl, got, c.want)
		}
	}
}

func TestIsIPv4(t *testing.T) {
	for ip, want := range map[string]bool{
		"1.2.3.4":         true,
		"182.54.236.60":   true,
		"":                false,
		"not-an-ip":       false,
		"::1":             false,
		"2001:db8::1":     false,
		"::ffff:1.2.3.4":  true, // 4-in-6 still maps to v4
	} {
		if got := isIPv4(ip); got != want {
			t.Errorf("isIPv4(%q) = %v, want %v", ip, got, want)
		}
	}
}

// fakeResolver lets us drive dnsblProbe through both branches without
// hitting real DNS.
type fakeResolver struct {
	hostErr     error
	hostAnswers []string
	txtErr      error
	txtAnswers  []string
}

func (f *fakeResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	return f.hostAnswers, f.hostErr
}
func (f *fakeResolver) LookupTXT(_ context.Context, _ string) ([]string, error) {
	return f.txtAnswers, f.txtErr
}

func TestDNSBLProbe_Branches(t *testing.T) {
	cases := []struct {
		name        string
		r           Resolver
		wantListed  bool
		wantDetail  string
	}{
		{
			"NXDOMAIN → not listed (the happy path)",
			&fakeResolver{hostErr: &net.DNSError{IsNotFound: true}},
			false, "",
		},
		{
			"resolver other error → treated as not listed (no false alarm)",
			&fakeResolver{hostErr: errors.New("SERVFAIL")},
			false, "",
		},
		{
			"A answer + TXT detail → listed with detail",
			&fakeResolver{
				hostAnswers: []string{"127.0.0.2"},
				txtAnswers:  []string{"https://www.spamhaus.org/query/ip/1.2.3.4"},
			},
			true, "https://www.spamhaus.org/query/ip/1.2.3.4",
		},
		{
			"A answer + no TXT → listed with empty detail",
			&fakeResolver{
				hostAnswers: []string{"127.0.0.2"},
				txtErr:      errors.New("no TXT"),
			},
			true, "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			save := mailRBLResolver
			mailRBLResolver = c.r
			t.Cleanup(func() { mailRBLResolver = save })

			listed, detail := dnsblProbe(context.Background(), "anything.invalid")
			if listed != c.wantListed {
				t.Errorf("listed = %v, want %v", listed, c.wantListed)
			}
			if detail != c.wantDetail {
				t.Errorf("detail = %q, want %q", detail, c.wantDetail)
			}
		})
	}
}
