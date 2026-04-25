package commands

import "testing"

const aptListUpgradableSample = `Listing...
curl/stable 8.5.0-2 amd64 [upgradable from: 8.4.0-2]
libc6/stable 2.36-9+deb12u4 amd64 [upgradable from: 2.36-9+deb12u3]
openssl/stable-security 3.0.13-1~deb12u1 amd64 [upgradable from: 3.0.11-1~deb12u2]
`

const aptListNoUpgradesSample = `Listing...
`

const aptListNoise = `Listing...
WARNING: apt does not have a stable CLI interface. Use with caution in scripts.
`

func TestParseAptUpgradable(t *testing.T) {
	pkgs := parseAptUpgradable(aptListUpgradableSample)
	if len(pkgs) != 3 {
		t.Fatalf("want 3 packages, got %d: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "curl" {
		t.Errorf("pkgs[0].Name = %q, want curl", pkgs[0].Name)
	}
	if pkgs[0].Current != "8.4.0-2" {
		t.Errorf("pkgs[0].Current = %q, want 8.4.0-2", pkgs[0].Current)
	}
	if pkgs[0].New != "8.5.0-2" {
		t.Errorf("pkgs[0].New = %q, want 8.5.0-2", pkgs[0].New)
	}
	if pkgs[0].Source != "stable" {
		t.Errorf("pkgs[0].Source = %q, want stable", pkgs[0].Source)
	}
	if pkgs[2].Source != "stable-security" {
		t.Errorf("pkgs[2].Source = %q, want stable-security", pkgs[2].Source)
	}
}

func TestParseAptUpgradable_Empty(t *testing.T) {
	pkgs := parseAptUpgradable(aptListNoUpgradesSample)
	if len(pkgs) != 0 {
		t.Fatalf("want 0 packages, got %d", len(pkgs))
	}
}

func TestParseAptUpgradable_IgnoresWarnings(t *testing.T) {
	pkgs := parseAptUpgradable(aptListNoise)
	if len(pkgs) != 0 {
		t.Fatalf("want 0 packages, got %d (warning lines must be skipped)", len(pkgs))
	}
}
