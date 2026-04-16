package certbot

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIssueHappyPath tests successful certificate issuance.
func TestIssueHappyPath(t *testing.T) {
	tmp := t.TempDir()
	runner := setupFakeCertbot(t, tmp, "success")

	result, err := runner.Issue("example.com", "/var/www/example", "admin@example.com", false)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.CertPath != filepath.Join(tmp, "letsencrypt/live/example.com/fullchain.pem") {
		t.Errorf("unexpected cert path: %s", result.CertPath)
	}
	if result.Skipped {
		t.Error("cert should not be skipped")
	}
	if result.IssuedAt.IsZero() {
		t.Error("issued_at should not be zero")
	}
	if result.ExpiresAt.IsZero() {
		t.Error("expires_at should not be zero")
	}
}

// TestIssueWithStaging tests issuance with --staging flag.
func TestIssueWithStaging(t *testing.T) {
	tmp := t.TempDir()
	runner := setupFakeCertbot(t, tmp, "success")

	result, err := runner.Issue("example.com", "/var/www/example", "admin@example.com", true)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.IssuedAt.Before(result.ExpiresAt) {
		t.Error("issued_at should be before expires_at")
	}
}

// TestRenewHappyPath tests successful certificate renewal.
func TestRenewHappyPath(t *testing.T) {
	tmp := t.TempDir()
	runner := setupFakeCertbot(t, tmp, "success")

	result, err := runner.Renew("example.com", false)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Skipped {
		t.Error("cert renewal should not be skipped")
	}
}

// TestRenewSkipped tests when renewal is skipped (cert not due).
func TestRenewSkipped(t *testing.T) {
	tmp := t.TempDir()
	runner := setupFakeCertbot(t, tmp, "skipped")

	result, err := runner.Renew("example.com", false)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.Skipped {
		t.Error("cert renewal should be skipped")
	}
}

// TestRevokeHappyPath tests successful certificate revocation.
func TestRevokeHappyPath(t *testing.T) {
	tmp := t.TempDir()
	runner := setupFakeCertbot(t, tmp, "success")

	result, err := runner.Revoke("example.com", "superseded")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

// TestClassifyStderrWebrootUnreachable tests error classification for webroot issues.
func TestClassifyStderrWebrootUnreachable(t *testing.T) {
	stderr := []byte("Error 404: Not Found while accessing webroot")
	reason := classifyStderr(stderr)
	if reason != "webroot_unreachable" {
		t.Errorf("expected 'webroot_unreachable', got '%s'", reason)
	}
}

// TestClassifyStderrRateLimited tests error classification for rate limiting.
func TestClassifyStderrRateLimited(t *testing.T) {
	stderr := []byte("Error: too many certificates already issued for exact set of domains")
	reason := classifyStderr(stderr)
	if reason != "rate_limited" {
		t.Errorf("expected 'rate_limited', got '%s'", reason)
	}
}

// TestClassifyStderrDNSFailed tests error classification for DNS failures.
func TestClassifyStderrDNSFailed(t *testing.T) {
	stderr := []byte("Error: DNS problem: NXDOMAIN looking up example.com")
	reason := classifyStderr(stderr)
	if reason != "dns_resolve_failed" {
		t.Errorf("expected 'dns_resolve_failed', got '%s'", reason)
	}
}

// TestClassifyStderrUnknown tests fallback for unknown errors.
func TestClassifyStderrUnknown(t *testing.T) {
	stderr := []byte("Some random error that we don't recognize")
	reason := classifyStderr(stderr)
	if reason != "unknown" {
		t.Errorf("expected 'unknown', got '%s'", reason)
	}
}

// TestTruncateStderr tests stderr truncation.
func TestTruncateStderr(t *testing.T) {
	longStderr := ""
	for i := 0; i < 1000; i++ {
		longStderr += "x"
	}
	truncated := truncateStderr(longStderr, 100)
	if len(truncated) != 100 {
		t.Errorf("expected truncated stderr to be 100 bytes, got %d", len(truncated))
	}
	// Should be the tail
	if truncated != longStderr[len(longStderr)-100:] {
		t.Error("truncated stderr should be the tail")
	}
}

// TestParseCertValidity tests certificate validity parsing.
func TestParseCertValidity(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "cert.pem")

	// Create a self-signed test certificate
	testCert := createTestCertPEM(t)
	if err := os.WriteFile(certPath, testCert, 0644); err != nil {
		t.Fatalf("failed to write test cert: %v", err)
	}

	issuedAt, expiresAt, err := ParseCertValidity(certPath)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issuedAt.IsZero() {
		t.Error("issued_at should not be zero")
	}
	if expiresAt.IsZero() {
		t.Error("expires_at should not be zero")
	}
	if !issuedAt.Before(expiresAt) {
		t.Error("issued_at should be before expires_at")
	}
}

// setupFakeCertbot creates a fake certbot binary and configures the runner.
func setupFakeCertbot(t *testing.T, tmp string, mode string) *Runner {
	certbotPath := filepath.Join(tmp, "certbot")

	// Create a minimal fake certbot script
	script := fmt.Sprintf(`#!/bin/bash
# Fake certbot for testing
action=$1
if [[ "$action" == "certonly" ]]; then
  domain="${6}"
  webroot="${4}"
  mkdir -p "%s/letsencrypt/live/$domain"
  # Create test certificate
  cat > "%s/letsencrypt/live/$domain/fullchain.pem" << 'EOF'
%s
EOF
  cat > "%s/letsencrypt/live/$domain/privkey.pem" << 'EOF'
-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg9r2Zcg3Q2D6+IRAc
1lixKYEJ6KFDCGv2GeLkF/sTrOGhRANCAATxBHvBCGCdtLTn5LmLVR8PJ/lDx8X/
ZRKRupd0Ey0vLHb7PkHYTJwXQxG3LpTdZ9EVYxGUAKW8gHdmxJP7SB1a
-----END PRIVATE KEY-----
EOF
  exit 0
elif [[ "$action" == "renew" ]]; then
  cert_name="${3}"
  if [[ "%s" == "skipped" ]]; then
    echo "Certificate not due for renewal" >&2
    mkdir -p "%s/letsencrypt/live/$cert_name"
    cat > "%s/letsencrypt/live/$cert_name/fullchain.pem" << 'EOF'
%s
EOF
  else
    mkdir -p "%s/letsencrypt/live/$cert_name"
    cat > "%s/letsencrypt/live/$cert_name/fullchain.pem" << 'EOF'
%s
EOF
  fi
  exit 0
elif [[ "$action" == "revoke" ]]; then
  exit 0
elif [[ "$action" == "delete" ]]; then
  exit 0
fi
exit 1
`, tmp, tmp, string(createTestCertPEM(t)), tmp, mode, tmp, tmp, string(createTestCertPEM(t)), tmp, tmp, string(createTestCertPEM(t)))

	if err := os.WriteFile(certbotPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create fake certbot: %v", err)
	}

	runner := NewRunner()
	runner.Binary = certbotPath
	runner.LERoot = filepath.Join(tmp, "letsencrypt")
	runner.OpenSSL = "openssl"

	return runner
}

// createTestCertPEM creates a self-signed certificate PEM for testing.
func createTestCertPEM(t *testing.T) []byte {
	// Generate private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Create certificate template
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "example.com",
		},
		NotBefore: now,
		NotAfter:  now.Add(90 * 24 * time.Hour), // 90-day cert like Let's Encrypt
		DNSNames:  []string{"example.com"},
	}

	// Self-sign
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create cert: %v", err)
	}

	// Encode to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return certPEM
}

// TestIssueError_Failure tests Issue when certbot fails.
func TestIssueError_Failure(t *testing.T) {
	runner := NewRunner()
	runner.Binary = "/nonexistent/certbot"

	result, err := runner.Issue("example.com", "/var/www/example", "admin@example.com", false)

	if err == nil {
		t.Fatal("expected error when certbot binary doesn't exist")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}
}

// TestRenewError_Failure tests Renew when certbot fails.
func TestRenewError_Failure(t *testing.T) {
	runner := NewRunner()
	runner.Binary = "/nonexistent/certbot"

	result, err := runner.Renew("example.com", false)

	if err == nil {
		t.Fatal("expected error when certbot binary doesn't exist")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}
}

// TestRenewError_WithForce tests Renew with force flag when it fails.
func TestRenewError_WithForce(t *testing.T) {
	runner := NewRunner()
	runner.Binary = "/nonexistent/certbot"

	result, err := runner.Renew("example.com", true)

	if err == nil {
		t.Fatal("expected error")
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

// TestRevokeError_Failure tests Revoke when certbot fails.
func TestRevokeError_Failure(t *testing.T) {
	runner := NewRunner()
	runner.Binary = "/nonexistent/certbot"

	result, err := runner.Revoke("example.com", "superseded")

	if err == nil {
		t.Fatal("expected error when certbot binary doesn't exist")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}
}

// TestParseCertValidity_MissingFile tests ParseCertValidity with non-existent file.
func TestParseCertValidity_MissingFile(t *testing.T) {
	_, _, err := ParseCertValidity("/nonexistent/path/cert.pem")

	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestParseCertValidity_InvalidPEM tests ParseCertValidity with invalid PEM content.
func TestParseCertValidity_InvalidPEM(t *testing.T) {
	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "invalid.pem")
	if err := os.WriteFile(certPath, []byte("not a valid certificate"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, _, err := ParseCertValidity(certPath)

	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

// TestTruncateStderr_LargeString tests truncation of large stderr.
func TestTruncateStderr_LargeString(t *testing.T) {
	large := "prefix " + strings.Repeat("x", 10000) + " suffix"
	result := truncateStderr(large, 100)

	if len(result) > 110 { // 100 + padding for "..."
		t.Errorf("expected truncated result, got %d chars", len(result))
	}
}
