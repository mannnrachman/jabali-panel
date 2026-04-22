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
//
// Hostnames lists extra SANs to include beyond the default
// [domain, www.domain] pair — used by M6.1 to add mail.<domain> +
// autoconfig.<domain> when email is enabled. Empty slice = legacy
// two-SAN behavior. Each hostname is validated against sslDomainRegex.
type sslSelfSignParams struct {
	Domain    string   `json:"domain"`
	Days      int      `json:"days"`
	Hostnames []string `json:"hostnames,omitempty"`
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
	for _, h := range p.Hostnames {
		if !sslDomainRegex.MatchString(h) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("invalid hostname %q in hostnames[]", h),
			}
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

	// Final SAN set: [domain, www.domain, ...hostnames], deduped, stable order.
	wantSANs := buildSelfSignSANs(p.Domain, p.Hostnames)

	// Reuse existing cert only when it's unexpired AND already covers the
	// full requested SAN set. Plain expiry wasn't enough before M6.1:
	// enabling email after issuance required adding mail/autoconfig SANs,
	// so a still-valid cert with the old two-SAN set would silently
	// miss the new hostnames if we kept the old "expiry-only" cache rule.
	if certExists, expiresAt, dnsNames := inspectExistingCert(certPath); certExists && expiresAt.After(time.Now()) && sansCoverRequested(dnsNames, wantSANs) {
		// Return existing cert info
		return sslSelfSignResponse{
			CertPath:  certPath,
			KeyPath:   keyPath,
			ExpiresAt: expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
		}, nil
	}

	// Generate new self-signed cert
	expiresAt, err := generateSelfSignedCert(p.Domain, days, certPath, keyPath, wantSANs)
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

// buildSelfSignSANs builds the final DNSNames list for a self-signed
// cert: [domain, www.domain, ...hostnames], deduped, in a stable order
// (domain first, then www, then hostnames in the order given).
func buildSelfSignSANs(domain string, extras []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 2+len(extras))
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(domain)
	add("www." + domain)
	for _, h := range extras {
		add(h)
	}
	return out
}

// sansCoverRequested returns true when every name in `want` appears
// somewhere in `have`. Used to decide whether an existing cert on disk
// covers the full SAN set or needs regeneration.
func sansCoverRequested(have, want []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, h := range have {
		set[h] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

// inspectExistingCert reads the cert at certPath and returns whether it
// exists, its expiry, and the DNSNames it covers. Returns (false, zero,
// nil) on any parse failure (treated as "regenerate").
func inspectExistingCert(certPath string) (bool, time.Time, []string) {
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		return false, time.Time{}, nil
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return false, time.Time{}, nil
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, time.Time{}, nil
	}

	return true, cert.NotAfter, cert.DNSNames
}

// generateSelfSignedCert creates an RSA-2048 self-signed certificate
// with the given DNS names. The first entry of `sans` is used as the
// Subject CN for operator readability in openssl/cert-inspection tools.
func generateSelfSignedCert(domain string, days int, certPath, keyPath string, sans []string) (time.Time, error) {
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

	if len(sans) == 0 {
		sans = []string{domain, "www." + domain}
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
		DNSNames: sans,
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
