package certbot

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Result contains the outcome of a certbot operation.
type Result struct {
	CertPath  string    `json:"cert_path"`
	KeyPath   string    `json:"key_path"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Skipped   bool      `json:"skipped"`
	Reason    string    `json:"reason"`
	Stderr    string    `json:"stderr,omitempty"`
}

// Runner manages certbot operations.
type Runner struct {
	Binary   string
	OpenSSL  string
	Env      []string
	LERoot   string
}

// NewRunner creates a default certbot runner.
func NewRunner() *Runner {
	return &Runner{
		Binary:  "certbot",
		OpenSSL: "openssl",
		Env:     nil,
		LERoot:  "/etc/letsencrypt",
	}
}

// Issue runs certbot to issue a new certificate.
func (r *Runner) Issue(domain, webroot, email string, staging bool) (*Result, error) {
	args := []string{
		"certonly",
		"--webroot",
		"-w", webroot,
		"-d", domain,
		"-m", email,
		"--agree-tos",
		"--non-interactive",
		"--keep-until-expiring",
	}

	if staging {
		args = append(args, "--staging")
	}

	cmd := exec.Command(r.Binary, args...)
	cmd.Env = r.Env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stderrText := stderr.String()

	// Classify the error if present
	reason := ""
	if err != nil {
		reason = classifyStderr([]byte(stderrText))
	}

	// Try to read the cert regardless of error — certbot may partially succeed
	cert, certErr := r.readCert(domain)
	if err != nil && certErr != nil {
		// Both issue and read failed
		stderrTail := truncateStderr(stderrText, 4096)
		return &Result{
			Reason: reason,
			Stderr: stderrTail,
		}, fmt.Errorf("certbot issue failed: %w", err)
	}

	if certErr != nil {
		// Issue succeeded (no error), but we can't read the cert
		stderrTail := truncateStderr(stderrText, 4096)
		return &Result{
			Reason: "unknown",
			Stderr: stderrTail,
		}, fmt.Errorf("failed to read issued certificate: %w", certErr)
	}

	// Success
	return &Result{
		CertPath:  cert.CertPath,
		KeyPath:   cert.KeyPath,
		IssuedAt:  cert.IssuedAt,
		ExpiresAt: cert.ExpiresAt,
		Skipped:   false,
		Reason:    "",
	}, nil
}

// Renew runs certbot to renew an existing certificate.
func (r *Runner) Renew(domain string, force bool) (*Result, error) {
	args := []string{
		"renew",
		"--cert-name", domain,
		"--non-interactive",
		"--no-random-sleep-on-renew",
	}

	if force {
		args = append(args, "--force-renewal")
	}

	cmd := exec.Command(r.Binary, args...)
	cmd.Env = r.Env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stderrText := stderr.String()

	// Check if renewal was skipped (exit 0 but no actual renewal happened)
	skipped := strings.Contains(stderrText, "not due for renewal")

	if err != nil {
		reason := classifyStderr([]byte(stderrText))
		stderrTail := truncateStderr(stderrText, 4096)
		return &Result{
			Reason: reason,
			Stderr: stderrTail,
		}, fmt.Errorf("certbot renew failed: %w", err)
	}

	// Read cert to get current validity
	cert, certErr := r.readCert(domain)
	if certErr != nil {
		stderrTail := truncateStderr(stderrText, 4096)
		return &Result{
			Skipped: skipped,
			Reason:  "unknown",
			Stderr:  stderrTail,
		}, fmt.Errorf("failed to read certificate after renew: %w", certErr)
	}

	return &Result{
		CertPath:  cert.CertPath,
		KeyPath:   cert.KeyPath,
		IssuedAt:  cert.IssuedAt,
		ExpiresAt: cert.ExpiresAt,
		Skipped:   skipped,
		Reason:    "",
	}, nil
}

// Revoke runs certbot to revoke a certificate.
func (r *Runner) Revoke(domain string, reason string) (*Result, error) {
	if reason == "" {
		reason = "unspecified"
	}

	args := []string{
		"revoke",
		"--cert-name", domain,
		"--reason", reason,
		"--non-interactive",
	}

	cmd := exec.Command(r.Binary, args...)
	cmd.Env = r.Env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
		stderrText := stderr.String()
		stderrTail := truncateStderr(stderrText, 4096)
		return &Result{
			Reason: classifyStderr([]byte(stderrText)),
			Stderr: stderrTail,
		}, fmt.Errorf("certbot revoke failed: %w", err)
	}

	// Delete the cert after revoking
	deleteCmd := exec.Command(r.Binary, "delete", "--cert-name", domain, "--non-interactive")
	deleteCmd.Env = r.Env

	var deleteStderr bytes.Buffer
	deleteCmd.Stderr = &deleteStderr

	if err := deleteCmd.Run(); err != nil {
		// Log but don't fail if delete fails; cert is already revoked
		fmt.Printf("warning: certbot delete failed after revoke: %v\n", err)
	}

	return &Result{
		Reason: "",
	}, nil
}

// readCert reads certificate validity from the issued cert PEM.
func (r *Runner) readCert(domain string) (*struct {
	CertPath  string
	KeyPath   string
	IssuedAt  time.Time
	ExpiresAt time.Time
}, error) {
	certPath := fmt.Sprintf("%s/live/%s/fullchain.pem", r.LERoot, domain)
	keyPath := fmt.Sprintf("%s/live/%s/privkey.pem", r.LERoot, domain)

	issuedAt, expiresAt, err := ParseCertValidity(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cert validity: %w", err)
	}

	return &struct {
		CertPath  string
		KeyPath   string
		IssuedAt  time.Time
		ExpiresAt time.Time
	}{
		CertPath:  certPath,
		KeyPath:   keyPath,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// ParseCertValidity parses the issued and expiration dates from a PEM certificate file.
func ParseCertValidity(pemPath string) (issuedAt, expiresAt time.Time, err error) {
	// Read the PEM file
	pemBytes, err := exec.Command("cat", pemPath).Output()
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to read cert file: %w", err)
	}

	// Parse the certificate using Go's x509 package
	cert, err := parsePEM(pemBytes)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to parse PEM: %w", err)
	}

	return cert.NotBefore, cert.NotAfter, nil
}

// parsePEM extracts the first certificate from a PEM block.
func parsePEM(pemBytes []byte) (*x509.Certificate, error) {
	// Use Go's pem package to decode the PEM block
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM certificate found")
	}

	// Parse the DER-encoded certificate
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("x509 parse failed: %w", err)
	}

	return cert, nil
}

// classifyStderr maps certbot errors to reason codes.
func classifyStderr(stderr []byte) string {
	stderrStr := string(stderr)

	patterns := map[string]string{
		"404.*Not Found|Fetching|webroot unreachable": "webroot_unreachable",
		"too many certificates already issued":       "rate_limited",
		"DNS problem.*NXDOMAIN":                       "dns_resolve_failed",
		"Invalid email address":                       "invalid_email",
		"Permission denied":                           "permission_denied",
	}

	for pattern, reason := range patterns {
		if matched, _ := regexp.MatchString(pattern, stderrStr); matched {
			return reason
		}
	}

	return "unknown"
}

// truncateStderr truncates stderr to a maximum size, keeping the tail.
func truncateStderr(stderr string, maxBytes int) string {
	if len(stderr) <= maxBytes {
		return stderr
	}
	// Keep the last maxBytes characters (the tail)
	return stderr[len(stderr)-maxBytes:]
}
