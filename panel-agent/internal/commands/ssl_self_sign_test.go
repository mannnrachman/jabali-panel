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
