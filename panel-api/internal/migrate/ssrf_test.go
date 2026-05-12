package migrate

import (
	"net"
	"strings"
	"testing"
)

func TestCheckIP_BlockedAlways(t *testing.T) {
	for _, ip := range []string{
		"127.0.0.1",
		"127.255.255.254",
		"169.254.169.254", // AWS/GCP metadata
		"::1",
		"fe80::1",
		"0.0.0.0",
	} {
		if err := checkIP(net.ParseIP(ip), true); err == nil {
			t.Errorf("expected %s blocked even with allowPrivate=true", ip)
		}
	}
}

func TestCheckIP_PrivateRespectsToggle(t *testing.T) {
	for _, ip := range []string{
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"fc00::1",
	} {
		if err := checkIP(net.ParseIP(ip), false); err == nil {
			t.Errorf("expected %s blocked with allowPrivate=false", ip)
		}
		if err := checkIP(net.ParseIP(ip), true); err != nil {
			// loopback still rejected even with allowPrivate; everything
			// else in this list is RFC1918/ULA so allowPrivate=true must
			// admit it.
			if !strings.Contains(err.Error(), "loopback") {
				t.Errorf("expected %s allowed with allowPrivate=true: %v", ip, err)
			}
		}
	}
}

func TestCheckIP_PublicAllowed(t *testing.T) {
	for _, ip := range []string{
		"1.1.1.1",
		"8.8.8.8",
		"2001:4860:4860::8888",
	} {
		if err := checkIP(net.ParseIP(ip), false); err != nil {
			t.Errorf("expected %s allowed: %v", ip, err)
		}
	}
}
