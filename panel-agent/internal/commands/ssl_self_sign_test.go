package commands

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestSSLSelfSignValidation_InvalidDomain(t *testing.T) {
	tests := []struct {
		domain string
		name   string
	}{
		{"../evil", "parent dir traversal"},
		{"..", "dot dot"},
		{"", "empty"},
		{"a" + string(make([]byte, 300)), "too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := sslSelfSignParams{
				Domain: tt.domain,
				Days:   365,
			}
			paramsJSON, _ := json.Marshal(params)

			result, err := sslSelfSignHandler(context.Background(), paramsJSON)

			if err == nil {
				t.Fatal("expected error for invalid domain")
			}
			if result != nil {
				t.Fatal("expected nil result")
			}
			if agentErr, ok := err.(*agentwire.AgentError); ok {
				if agentErr.Code != agentwire.CodeInvalidArgument {
					t.Errorf("expected CodeInvalidArgument, got %s", agentErr.Code)
				}
			} else {
				t.Fatal("expected AgentError")
			}
		})
	}
}

func TestSSLSelfSignValidation_ValidParams(t *testing.T) {
	tests := []struct {
		domain string
		name   string
	}{
		{"example.com", "simple domain"},
		{"sub.example.com", "subdomain"},
		{"example.co.uk", "tld with dot"},
		{"a.b.c.d.example.com", "multiple subdomains"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !sslDomainRegex.MatchString(tt.domain) {
				t.Fatalf("domain %q should match regex but doesn't", tt.domain)
			}
		})
	}
}

func TestSSLSelfSign_HappyPath(t *testing.T) {
	// Use temp dir for testing
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() {
		baseSelfSignDir = oldBaseSelfSignDir
	})

	domain := "example.com"
	params := sslSelfSignParams{
		Domain: domain,
		Days:   365,
	}
	paramsJSON, _ := json.Marshal(params)

	result, err := sslSelfSignHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	resp, ok := result.(sslSelfSignResponse)
	if !ok {
		t.Fatalf("expected sslSelfSignResponse, got %T", result)
	}

	// Verify paths
	expectedCertDir := filepath.Join(tempDir, domain)
	expectedCertPath := filepath.Join(expectedCertDir, "fullchain.pem")
	expectedKeyPath := filepath.Join(expectedCertDir, "privkey.pem")

	if resp.CertPath != expectedCertPath {
		t.Errorf("cert path mismatch: got %q, expected %q", resp.CertPath, expectedCertPath)
	}
	if resp.KeyPath != expectedKeyPath {
		t.Errorf("key path mismatch: got %q, expected %q", resp.KeyPath, expectedKeyPath)
	}

	// Verify files exist
	if _, err := os.Stat(resp.CertPath); err != nil {
		t.Fatalf("cert file does not exist: %v", err)
	}
	if _, err := os.Stat(resp.KeyPath); err != nil {
		t.Fatalf("key file does not exist: %v", err)
	}

	// Verify cert is parseable and has correct domain
	certBytes, _ := os.ReadFile(resp.CertPath)
	block, _ := pem.Decode(certBytes)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse cert: %v", err)
	}

	if cert.Subject.CommonName != domain {
		t.Errorf("CN mismatch: got %q, expected %q", cert.Subject.CommonName, domain)
	}

	// Verify expiry is roughly 365 days
	expiresAt, _ := time.Parse("2006-01-02T15:04:05Z", resp.ExpiresAt)
	now := time.Now()
	daysToExpiry := int(expiresAt.Sub(now).Hours() / 24)
	if daysToExpiry < 360 || daysToExpiry > 365 {
		t.Errorf("expiry days off: got %d, expected ~365", daysToExpiry)
	}
}

func TestSSLSelfSign_Idempotency(t *testing.T) {
	// Use temp dir for testing
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() {
		baseSelfSignDir = oldBaseSelfSignDir
	})

	domain := "example.com"
	params := sslSelfSignParams{
		Domain: domain,
		Days:   365,
	}
	paramsJSON, _ := json.Marshal(params)

	// First call
	result1, err := sslSelfSignHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	resp1 := result1.(sslSelfSignResponse)

	// Get file contents
	certBytes1, _ := os.ReadFile(resp1.CertPath)
	keyBytes1, _ := os.ReadFile(resp1.KeyPath)

	// Second call (should reuse)
	result2, err := sslSelfSignHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	resp2 := result2.(sslSelfSignResponse)

	// Get file contents
	certBytes2, _ := os.ReadFile(resp2.CertPath)
	keyBytes2, _ := os.ReadFile(resp2.KeyPath)

	// Files should be identical (idempotent)
	if string(certBytes1) != string(certBytes2) {
		t.Error("cert was regenerated instead of reused")
	}
	if string(keyBytes1) != string(keyBytes2) {
		t.Error("key was regenerated instead of reused")
	}

	// Response should match
	if resp1.ExpiresAt != resp2.ExpiresAt {
		t.Error("expiry times should match for reused cert")
	}
}

func TestSSLSelfSign_ReusesValidCert(t *testing.T) {
	// Use temp dir for testing
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() {
		baseSelfSignDir = oldBaseSelfSignDir
	})

	domain := "example.com"
	params := sslSelfSignParams{
		Domain: domain,
		Days:   365,
	}
	paramsJSON, _ := json.Marshal(params)

	// First call
	result1, err := sslSelfSignHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	resp1 := result1.(sslSelfSignResponse)

	// Get original cert
	certBytes1, _ := os.ReadFile(resp1.CertPath)

	// Second call with same domain (should reuse existing valid cert)
	result2, err := sslSelfSignHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	resp2 := result2.(sslSelfSignResponse)

	// Get cert from second call
	certBytes2, _ := os.ReadFile(resp2.CertPath)

	// Certificates should be identical (reused, not regenerated)
	if string(certBytes1) != string(certBytes2) {
		t.Error("valid cert should have been reused, not regenerated")
	}

	// Responses should match
	if resp1.ExpiresAt != resp2.ExpiresAt {
		t.Error("expiry times should match for reused cert")
	}
}

func TestSSLSelfSign_ExtraHostnamesIncludedInSANs(t *testing.T) {
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() { baseSelfSignDir = oldBaseSelfSignDir })

	params := sslSelfSignParams{
		Domain:    "example.com",
		Days:      365,
		Hostnames: []string{"mail.example.com", "autoconfig.example.com"},
	}
	paramsJSON, _ := json.Marshal(params)
	result, err := sslSelfSignHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	resp := result.(sslSelfSignResponse)

	certBytes, _ := os.ReadFile(resp.CertPath)
	block, _ := pem.Decode(certBytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	want := []string{"example.com", "www.example.com", "mail.example.com", "autoconfig.example.com"}
	if len(cert.DNSNames) != len(want) {
		t.Fatalf("DNSNames len: got %d (%v), want %d (%v)", len(cert.DNSNames), cert.DNSNames, len(want), want)
	}
	for i, w := range want {
		if cert.DNSNames[i] != w {
			t.Errorf("DNSNames[%d]: got %q, want %q", i, cert.DNSNames[i], w)
		}
	}
}

func TestSSLSelfSign_RegeneratesOnSANExpansion(t *testing.T) {
	// Covers the M6.1 cache-invalidation bug: a still-valid cert with
	// an outdated SAN set (e.g. issued before email_enable added
	// mail.<domain>) must be regenerated, not reused.
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() { baseSelfSignDir = oldBaseSelfSignDir })

	// First issuance: no extras.
	p1, _ := json.Marshal(sslSelfSignParams{Domain: "example.com", Days: 365})
	r1, err := sslSelfSignHandler(context.Background(), p1)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	cert1Bytes, _ := os.ReadFile(r1.(sslSelfSignResponse).CertPath)

	// Second issuance: SAN set grows. Must regenerate.
	p2, _ := json.Marshal(sslSelfSignParams{
		Domain:    "example.com",
		Days:      365,
		Hostnames: []string{"mail.example.com"},
	})
	r2, err := sslSelfSignHandler(context.Background(), p2)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	cert2Bytes, _ := os.ReadFile(r2.(sslSelfSignResponse).CertPath)

	if string(cert1Bytes) == string(cert2Bytes) {
		t.Fatal("cert was reused but SAN set grew; should have regenerated")
	}

	block, _ := pem.Decode(cert2Bytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse regen cert: %v", err)
	}
	found := false
	for _, n := range cert.DNSNames {
		if n == "mail.example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("regenerated cert missing mail.example.com; got %v", cert.DNSNames)
	}
}

func TestSSLSelfSign_ReusesWhenSANSubsetIsSame(t *testing.T) {
	// Same-SAN-set second call still hits the reuse path.
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() { baseSelfSignDir = oldBaseSelfSignDir })

	params := sslSelfSignParams{
		Domain:    "example.com",
		Days:      365,
		Hostnames: []string{"mail.example.com"},
	}
	pJSON, _ := json.Marshal(params)

	r1, err := sslSelfSignHandler(context.Background(), pJSON)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	c1, _ := os.ReadFile(r1.(sslSelfSignResponse).CertPath)

	r2, err := sslSelfSignHandler(context.Background(), pJSON)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	c2, _ := os.ReadFile(r2.(sslSelfSignResponse).CertPath)

	if string(c1) != string(c2) {
		t.Error("cert should have been reused (same SAN set, not expired)")
	}
}

func TestSSLSelfSign_InvalidHostname(t *testing.T) {
	tempDir := t.TempDir()
	oldBaseSelfSignDir := baseSelfSignDir
	baseSelfSignDir = tempDir
	t.Cleanup(func() { baseSelfSignDir = oldBaseSelfSignDir })

	params := sslSelfSignParams{
		Domain:    "example.com",
		Days:      365,
		Hostnames: []string{"bad_underscore"},
	}
	pJSON, _ := json.Marshal(params)
	_, err := sslSelfSignHandler(context.Background(), pJSON)
	if err == nil {
		t.Fatal("expected error for invalid hostname in hostnames[]")
	}
	agentErr, ok := err.(*agentwire.AgentError)
	if !ok || agentErr.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument, got %+v", err)
	}
}

func TestBuildSelfSignSANs(t *testing.T) {
	got := buildSelfSignSANs("example.com", []string{"mail.example.com", "example.com", "autoconfig.example.com", "www.example.com"})
	want := []string{"example.com", "www.example.com", "mail.example.com", "autoconfig.example.com"}
	if len(got) != len(want) {
		t.Fatalf("dedup len: got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("order: got[%d]=%q want %q (full: %v)", i, got[i], w, got)
		}
	}
}

func TestSSLSelfSignCommandRegistered(t *testing.T) {
	handlers := Default.Commands()
	found := false
	for _, cmd := range handlers {
		if cmd == "ssl.self_sign" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ssl.self_sign command not registered")
	}
}
