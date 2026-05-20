package commands

import (
	"strings"
	"testing"
)

func TestValidateMTAStsApply(t *testing.T) {
	good := mtaStsApplyParams{
		Domain:  "example.com",
		MXHost:  "mx.example.com",
		Mode:    "testing",
		MaxAge:  604800,
		SSLCert: "/etc/letsencrypt/live/example.com/fullchain.pem",
		SSLKey:  "/etc/letsencrypt/live/example.com/privkey.pem",
	}
	if err := validateMTAStsApply(good); err != nil {
		t.Fatalf("good params rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(p *mtaStsApplyParams)
	}{
		{"bad domain", func(p *mtaStsApplyParams) { p.Domain = "Bad-_-Domain" }},
		{"empty domain", func(p *mtaStsApplyParams) { p.Domain = "" }},
		{"bad mx host", func(p *mtaStsApplyParams) { p.MXHost = "INVALID HOST" }},
		{"bad mode", func(p *mtaStsApplyParams) { p.Mode = "force" }},
		{"max_age too low", func(p *mtaStsApplyParams) { p.MaxAge = 3600 }},
		{"max_age too high", func(p *mtaStsApplyParams) { p.MaxAge = 100000000 }},
		{"relative cert path", func(p *mtaStsApplyParams) { p.SSLCert = "etc/cert.pem" }},
		{"traversal in key", func(p *mtaStsApplyParams) { p.SSLKey = "/etc/../shadow" }},
		{"empty cert", func(p *mtaStsApplyParams) { p.SSLCert = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := good
			c.mut(&p)
			if err := validateMTAStsApply(p); err == nil {
				t.Fatalf("expected rejection for %s", c.name)
			}
		})
	}
}

func TestRenderMTAStsPolicy(t *testing.T) {
	got := renderMTAStsPolicy("testing", 604800, "mx.example.com")
	want := "version: STSv1\nmode: testing\nmax_age: 604800\nmx: mx.example.com\n"
	if got != want {
		t.Errorf("policy mismatch\ngot:\n%q\nwant:\n%q", got, want)
	}
	// Enforce mode round-trip.
	if !strings.Contains(renderMTAStsPolicy("enforce", 86400, "h.x"), "mode: enforce") {
		t.Error("enforce mode missing")
	}
}

func TestRenderMTAStsVhost(t *testing.T) {
	got := renderMTAStsVhost(
		"example.com",
		"/var/www/jabali-mta-sts/example.com",
		"/etc/letsencrypt/live/example.com/fullchain.pem",
		"/etc/letsencrypt/live/example.com/privkey.pem",
	)
	required := []string{
		"server_name mta-sts.example.com",
		"listen 443 ssl http2",
		"listen [::]:443 ssl http2",
		"ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem",
		"location = /.well-known/mta-sts.txt",
		"root /var/www/jabali-mta-sts/example.com",
		"# Managed by jabali agent",
	}
	for _, r := range required {
		if !strings.Contains(got, r) {
			t.Errorf("vhost missing %q", r)
		}
	}
	// Must NOT serve plain HTTP.
	if strings.Contains(got, "listen 80") {
		t.Error("vhost listens on :80 — MTA-STS requires https only (RFC 8461 §3.3)")
	}
}
