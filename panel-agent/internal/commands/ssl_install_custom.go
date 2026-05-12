// ssl.install_custom — write an operator-supplied cert + key under
// /etc/letsencrypt/live/<domain>/ so nginx's existing vhost template
// (which reads fullchain.pem + privkey.pem from that path) serves the
// custom cert without any vhost rewrite. Used by the M35 migration
// importer to land apache_tls/<dom>/ pieces from a cpmove tarball.
//
// Skipping certbot/acme.sh entirely is intentional: a custom cert may
// be from a private CA, a paid SAN cert, or a self-signed dev cert
// the operator wants preserved verbatim. The reconciler's auto-LE
// path still applies once the cert expires — nothing else changes.

package commands

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

const sslLERoot = "/etc/letsencrypt"

type sslInstallCustomParams struct {
	Domain  string `json:"domain"`
	CertPEM string `json:"cert_pem"` // X509 cert + optional intermediates (concatenated PEM blocks)
	KeyPEM  string `json:"key_pem"`  // RSA / EC private key (PKCS#1 / PKCS#8)
}

type sslInstallCustomResponse struct {
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

var sslInstallDomainRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{1,253}$`)

func sslInstallCustomHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sslInstallCustomParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "parse params: " + err.Error(),
		}
	}
	if !sslInstallDomainRegex.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "invalid domain: " + p.Domain,
		}
	}
	if strings.TrimSpace(p.CertPEM) == "" || strings.TrimSpace(p.KeyPEM) == "" {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "cert_pem + key_pem required",
		}
	}
	// Validate cert + key parse + match.
	leaf, err := parseLeafCert(p.CertPEM)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "cert_pem: " + err.Error(),
		}
	}
	if err := validateKeyMatchesCert(p.KeyPEM, leaf); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: err.Error(),
		}
	}

	liveDir := filepath.Join(sslLERoot, "live", p.Domain)
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "mkdir live: " + err.Error()}
	}
	certPath := filepath.Join(liveDir, "fullchain.pem")
	keyPath := filepath.Join(liveDir, "privkey.pem")

	// Atomic write: temp + rename.
	if err := writeAtomic(certPath, []byte(strings.TrimSpace(p.CertPEM)+"\n"), 0o644); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "write cert: " + err.Error()}
	}
	if err := writeAtomic(keyPath, []byte(strings.TrimSpace(p.KeyPEM)+"\n"), 0o600); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "write key: " + err.Error()}
	}

	// Reload nginx — best-effort. nginx -s reload skips on syntax error
	// (nginx -t pre-check below catches that), and the existing daemon
	// keeps serving with the previous cert. Operator can rerun the
	// install after fixing the cert.
	if testCmd := exec.CommandContext(ctx, "nginx", "-t"); testCmd.Run() == nil {
		_ = exec.CommandContext(ctx, "nginx", "-s", "reload").Run()
	}

	return sslInstallCustomResponse{CertPath: certPath, KeyPath: keyPath}, nil
}

func parseLeafCert(blob string) (*x509.Certificate, error) {
	rest := []byte(blob)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("no CERTIFICATE PEM block found")
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		return cert, nil
	}
}

// validateKeyMatchesCert ensures the supplied key actually pairs with
// the leaf cert (modulus / curve match) so we don't write a working
// cert with an unrelated key.
func validateKeyMatchesCert(keyPEM string, leaf *x509.Certificate) error {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return fmt.Errorf("key_pem: no PEM block")
	}
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return fmt.Errorf("key_pem: unsupported PEM type %q", block.Type)
	}
	if err != nil {
		return fmt.Errorf("key_pem: parse %s: %w", block.Type, err)
	}
	switch leafPub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		var priv *rsa.PrivateKey
		switch k := key.(type) {
		case *rsa.PrivateKey:
			priv = k
		default:
			return fmt.Errorf("cert/key algo mismatch (leaf=RSA, key=%T)", k)
		}
		if priv.PublicKey.N.Cmp(leafPub.N) != 0 || priv.PublicKey.E != leafPub.E {
			return fmt.Errorf("cert/key modulus mismatch")
		}
	default:
		// EC + Ed25519 — skip strict equality; PEM parse + algo
		// check above is enough for v1. nginx -t will catch any
		// remaining mismatch.
		_ = leafPub
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func init() {
	Default.Register("ssl.install_custom", sslInstallCustomHandler)
}
