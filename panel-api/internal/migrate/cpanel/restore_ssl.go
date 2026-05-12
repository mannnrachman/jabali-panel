// restore_ssl.go — M35.8 P3: scan the cpmove tarball for per-domain
// SSL pieces + push them into /etc/letsencrypt/live/<domain>/ via
// the agent's ssl.install_custom command. cpanel writes the user's
// installed certs under apache_tls/<domain>/ inside the cpmove
// wrapper:
//
//   apache_tls/<domain>/combined        — cert + chain + key concatenated
//   apache_tls/<domain>/certificates    — cert + chain (no key)
//   apache_tls/<domain>/key             — private key only
//
// The legacy full-backup layout omits the wrapper. Both are handled.
//
// Idempotent: re-running on a destination that already serves the
// same cert just overwrites with the identical bytes (atomic rename).

package cpanel

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

type SSLResult struct {
	Installed int      // per-domain certs successfully pushed
	Skipped   []string // human-readable reasons (path missing, parse fail, …)
}

func ImportSSL(ctx context.Context, agentCli agent.AgentInterface, parsed *ParsedTarball) (*SSLResult, error) {
	if parsed == nil {
		return nil, errors.New("ImportSSL: parsed nil")
	}
	res := &SSLResult{}
	if agentCli == nil {
		res.Skipped = append(res.Skipped, "ssl_skip:agent_unwired")
		return res, nil
	}

	roots := []string{
		filepath.Join(parsed.ExtractDir, "cpmove-"+parsed.SourceUser, "apache_tls"),
		filepath.Join(parsed.ExtractDir, "apache_tls"),
		filepath.Join(parsed.ExtractDir, "cp", parsed.SourceUser, "apache_tls"),
	}
	var apacheTLS string
	for _, r := range roots {
		if info, err := os.Stat(r); err == nil && info.IsDir() {
			apacheTLS = r
			break
		}
	}
	if apacheTLS == "" {
		// No installed certs on source — fall through silently. The
		// reconciler's auto-LE path picks up the (domain, vhost)
		// rows ImportDomains created.
		return res, nil
	}

	entries, err := os.ReadDir(apacheTLS)
	if err != nil {
		res.Skipped = append(res.Skipped, fmt.Sprintf("ssl_read_apache_tls:%v", err))
		return res, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		domain := e.Name()
		domainDir := filepath.Join(apacheTLS, domain)
		certPEM, keyPEM, sk := readDomainSSL(domainDir)
		if certPEM == "" || keyPEM == "" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("ssl_skip:%s:missing cert or key", domain))
			res.Skipped = append(res.Skipped, sk...)
			continue
		}
		if _, callErr := agentCli.Call(ctx, "ssl.install_custom", map[string]any{
			"domain":   domain,
			"cert_pem": certPEM,
			"key_pem":  keyPEM,
		}); callErr != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("ssl_skip:%s:agent:%v", domain, callErr))
			continue
		}
		res.Installed++
		res.Skipped = append(res.Skipped, sk...)
	}
	return res, nil
}

// readDomainSSL returns (certPEM, keyPEM, warnings) for one
// apache_tls/<domain>/ directory. Tries `combined` first (split the
// PEM blocks into cert vs key by type), falls back to
// `certificates` + `key`.
func readDomainSSL(domainDir string) (string, string, []string) {
	var warnings []string

	combined, err := os.ReadFile(filepath.Join(domainDir, "combined"))
	if err == nil && len(combined) > 0 {
		certBlocks, keyBlocks := splitPEMByType(combined)
		return certBlocks, keyBlocks, warnings
	}

	certBytes, certErr := os.ReadFile(filepath.Join(domainDir, "certificates"))
	keyBytes, keyErr := os.ReadFile(filepath.Join(domainDir, "key"))
	if certErr != nil {
		warnings = append(warnings, fmt.Sprintf("ssl_read_cert:%s:%v", domainDir, certErr))
	}
	if keyErr != nil {
		warnings = append(warnings, fmt.Sprintf("ssl_read_key:%s:%v", domainDir, keyErr))
	}
	return strings.TrimSpace(string(certBytes)), strings.TrimSpace(string(keyBytes)), warnings
}

// splitPEMByType walks every PEM block in `raw` + emits two
// strings: one concatenating CERTIFICATE blocks (leaf + chain) and
// one concatenating PRIVATE KEY blocks (RSA / EC / PKCS#8).
func splitPEMByType(raw []byte) (string, string) {
	var certs, keys strings.Builder
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			_ = pem.Encode(&certs, block)
		case "RSA PRIVATE KEY", "EC PRIVATE KEY", "PRIVATE KEY":
			_ = pem.Encode(&keys, block)
		}
	}
	return strings.TrimSpace(certs.String()), strings.TrimSpace(keys.String())
}
