// dkim.import — write a legacy DKIM private key (e.g. cpanel's
// RSA selector) into a sidecar path so the operator can hand-
// configure Stalwart to honor the original DKIM signature. jabali's
// own DKIM path stays Ed25519 with selector "jabali"; the imported
// key is for verification continuity of pre-migration mail.
//
// Path: /etc/jabali-panel/dkim/legacy/<domain>/{<selector>.key,info}
// The `info` file records the source selector + algorithm so
// the operator (or a future Stalwart-config-write helper) knows
// what to publish.

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

const dkimLegacyDir = "/etc/jabali-panel/dkim/legacy"

type dkimImportParams struct {
	Domain    string `json:"domain"`
	Selector  string `json:"selector"`  // e.g. "default" (cpanel)
	Algorithm string `json:"algorithm"` // "rsa" | "ed25519"
	KeyPEM    string `json:"key_pem"`
}

type dkimImportResponse struct {
	KeyPath  string `json:"key_path"`
	InfoPath string `json:"info_path"`
}

var dkimImportDomainRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{1,253}$`)
var dkimImportSelectorRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func dkimImportHandler(_ context.Context, params json.RawMessage) (any, error) {
	var p dkimImportParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "parse params: " + err.Error(),
		}
	}
	if !dkimImportDomainRe.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "invalid domain: " + p.Domain,
		}
	}
	if p.Selector == "" {
		p.Selector = "default"
	}
	if !dkimImportSelectorRe.MatchString(p.Selector) {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "invalid selector: " + p.Selector,
		}
	}
	if strings.TrimSpace(p.KeyPEM) == "" {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "key_pem required",
		}
	}
	algo := strings.ToLower(strings.TrimSpace(p.Algorithm))
	if algo == "" {
		algo = "rsa"
	}

	dir := filepath.Join(dkimLegacyDir, p.Domain)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "mkdir: " + err.Error()}
	}
	keyPath := filepath.Join(dir, p.Selector+".key")
	infoPath := filepath.Join(dir, "info")

	if err := writeAtomic(keyPath, []byte(strings.TrimSpace(p.KeyPEM)+"\n"), 0o600); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "write key: " + err.Error()}
	}
	info := fmt.Sprintf("selector=%s\nalgorithm=%s\nsource=m35-migration\n", p.Selector, algo)
	if err := writeAtomic(infoPath, []byte(info), 0o640); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "write info: " + err.Error()}
	}
	return dkimImportResponse{KeyPath: keyPath, InfoPath: infoPath}, nil
}

func init() {
	Default.Register("dkim.import", dkimImportHandler)
}
