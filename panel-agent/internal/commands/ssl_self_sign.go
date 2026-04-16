package commands

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// sslSelfSignParams is the input shape for ssl.self_sign.
type sslSelfSignParams struct {
	Domain string `json:"domain"`
	Days   int    `json:"days"`
}

// sslSelfSignResponse is the output shape for ssl.self_sign.
type sslSelfSignResponse struct {
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	ExpiresAt string `json:"expires_at"`
}

// baseSelfSignDir is the base directory for self-signed certs. Can be overridden in tests.
var baseSelfSignDir = "/etc/ssl/jabali-selfsigned"

func sslSelfSignHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sslSelfSignParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate domain format
	if !sslDomainRegex.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid domain %q: must match ^[a-zA-Z0-9][a-zA-Z0-9.-]{1,253}$", p.Domain),
		}
	}

	// Default to 365 days if not specified or invalid
	days := p.Days
	if days <= 0 {
		days = 365
	}

	// Ensure cert directory exists
	certDir := filepath.Join(baseSelfSignDir, p.Domain)
	if err := os.MkdirAll(certDir, 0755); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to create cert directory: %v", err),
		}
	}

	certPath := filepath.Join(certDir, "fullchain.pem")
	keyPath := filepath.Join(certDir, "privkey.pem")

	// Check if cert + key already exist and are not expired
	if certExists, expiresAt := checkExistingCert(certPath); certExists && expiresAt.After(time.Now()) {
		// Return existing cert info
		return sslSelfSignResponse{
			CertPath:  certPath,
			KeyPath:   keyPath,
			ExpiresAt: expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		}, nil
	}

	// Generate new self-signed cert
	expiresAt, err := generateSelfSignedCert(p.Domain, days, certPath, keyPath)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("failed to generate self-signed cert: %v", err),
		}
	}

	return sslSelfSignResponse{
		CertPath:  certPath,
		KeyPath:   keyPath,
		ExpiresAt: expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

// checkExistingCert checks if cert and key exist and returns the expiry time.
// Returns (true, expiryTime) if cert exists, (false, zero) otherwise.
func checkExistingCert(certPath string) (bool, time.Time) {
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return false, time.Time{}
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return false, time.Time{}
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, time.Time{}
	}

	return true, cert.NotAfter
}

// generateSelfSignedCert creates an RSA-2048 self-signed certificate.
func generateSelfSignedCert(domain string, days int, certPath, keyPath string) (time.Time, error) {
	// Generate RSA private key
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Create certificate template
	now := time.Now()
	notAfter := now.AddDate(0, 0, days)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: domain,
		},
		NotBefore: now,
		NotAfter:  notAfter,
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		DNSNames: []string{domain, "www." + domain},
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate to file
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open cert file: %w", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	}); err != nil {
		return time.Time{}, fmt.Errorf("failed to write cert: %w", err)
	}

	// Write private key to file
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open key file: %w", err)
	}
	defer keyFile.Close()

	privKeyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := pem.Encode(keyFile, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyDER,
	}); err != nil {
		return time.Time{}, fmt.Errorf("failed to write key: %w", err)
	}

	return notAfter, nil
}

func init() {
	Default.Register("ssl.self_sign", sslSelfSignHandler)
}
