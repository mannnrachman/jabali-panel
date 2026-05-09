// domain.email_dkim_rotate — rotate the DKIM ed25519 keypair for
// one mail-enabled domain (ADR-0043 §"Rotation").
//
// Operator-driven: a v1 cron-driven auto-rotation isn't shipped
// (DKIM rotation is rare + requires DNS re-publication; auto-rotate
// without operator awareness can break inbound mail when the new
// TXT hasn't propagated). Triggered via:
//   jabali domain email-dkim-rotate <domain>
//
// Rotation steps:
//   1. Snapshot the existing private key to <domain>.key.old (so
//      the operator can roll back via shell if the new TXT hasn't
//      propagated when verifiers query)
//   2. Generate fresh ed25519 keypair via dkim.GenerateEd25519
//   3. Atomically write the new private key (dkim.WritePrivate
//      uses a tmp + rename so a partial write can't leave Stalwart
//      reading a half-written key)
//   4. Reload Stalwart so it re-reads the new key on next sign
//   5. Return both old + new public TXT values so the panel-side
//      caller can update domain.dkim_public_key + push the new
//      TXT record into pdns
//
// The old .key.old file persists across reboots until the operator
// runs `rm /etc/jabali-panel/dkim/<domain>.key.old`. ADR-0043
// §"Rotation" notes the operator decides when DNS propagation is
// stable; the .old file is the rollback path.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/dkim"
)

type domainEmailDKIMRotateParams struct {
	DomainName string `json:"domain_name"`
}

type domainEmailDKIMRotateResponse struct {
	OldDKIMPublicKey string `json:"old_dkim_public_key,omitempty"`
	NewDKIMPublicKey string `json:"new_dkim_public_key"`
	OldKeyBackupPath string `json:"old_key_backup_path,omitempty"`
}

func init() {
	Default.Register("domain.email_dkim_rotate", domainEmailDKIMRotateHandler)
}

func domainEmailDKIMRotateHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p domainEmailDKIMRotateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "malformed JSON: " + err.Error(),
		}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument, Message: "domain_name required",
		}
	}
	if err := validateDomainNameForShell(p.DomainName); err != nil {
		return nil, err
	}

	keyPath := filepath.Join(dkimKeyDirFunc(), p.DomainName+".key")
	oldKeyPath := keyPath + ".old"

	// Read the existing key first so we can return both old + new
	// TXT values — operator's runbook §rollback uses the old TXT
	// to verify the .old file restores cleanly.
	resp := domainEmailDKIMRotateResponse{}
	existingSeed, err := dkim.LoadEd25519(keyPath)
	if err != nil {
		// Missing existing key: rotation on a not-yet-enabled domain
		// is nonsensical. Refuse with a clear pointer rather than
		// silently generating a fresh one (which the email_enable
		// handler already does idempotently).
		if errors.Is(err, os.ErrNotExist) {
			return nil, &agentwire.AgentError{
				Code: agentwire.CodeFailedPrecondition,
				Message: fmt.Sprintf(
					"no existing DKIM key at %s — run domain.email_enable first; rotation only applies to already-enabled domains",
					keyPath),
			}
		}
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("load existing DKIM key %s: %v", keyPath, err),
		}
	}
	oldTXT, err := dkim.PublicDKIMTxtFromSeed(existingSeed)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("derive old DKIM TXT: %v", err),
		}
	}
	resp.OldDKIMPublicKey = string(oldTXT)

	// Snapshot the old key to <domain>.key.old before generating
	// the new one. Pure file copy — operator's rollback path is
	// `mv <domain>.key.old <domain>.key && systemctl reload
	// jabali-stalwart`.
	if err := copyFileMode(keyPath, oldKeyPath, 0o600); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("snapshot old DKIM key to %s: %v", oldKeyPath, err),
		}
	}
	resp.OldKeyBackupPath = oldKeyPath

	// Generate fresh keypair + atomic-write.
	kp, err := dkim.GenerateEd25519()
	if err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("generate fresh DKIM key: %v", err),
		}
	}
	if err := dkim.WritePrivate(keyPath, kp.PrivateRawBase64); err != nil {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInternal,
			Message: fmt.Sprintf("write fresh DKIM key %s: %v", keyPath, err),
		}
	}

	// Reload Stalwart so it picks up the new key on the next sign.
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if out, err := runSystemctl(rctx, "reload", "jabali-stalwart.service"); err != nil {
		if !isReloadNotSupportedErr(out) {
			// Non-fatal but surface clearly: new key is on disk but
			// Stalwart hasn't reread it yet. Operator can manually
			// systemctl reload jabali-stalwart, or the next signing
			// op picks it up at process restart.
			return nil, &agentwire.AgentError{
				Code: agentwire.CodeInternal,
				Message: fmt.Sprintf(
					"DKIM key rotated on disk but Stalwart reload failed: %v (%s) — keys at %s + %s",
					err, bytesTrim(out), keyPath, oldKeyPath),
			}
		}
	}

	resp.NewDKIMPublicKey = string(kp.PublicDKIMTxt)
	return resp, nil
}

// copyFileMode does a straight copy from src to dst with the given
// dst file mode. Uses io.Copy; chmod ensures perms even on a fresh
// create. Used by the rotation handler to snapshot the old key.
func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, mode); err != nil {
		return err
	}
	return nil
}
