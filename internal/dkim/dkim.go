// Package dkim generates Ed25519 DKIM keypairs (RFC 8463) for panel-owned
// signing of outbound mail.
//
// Shared by panel-api (reconciler uses this for rotation — M6.1+) and
// panel-agent (domain.email_enable handler generates the per-domain key).
//
// On-disk format is the 32-byte Ed25519 seed, standard-base64-encoded,
// followed by a single `\n`. NOT PKCS#8 PEM — Stalwart's SQL/config
// loader expects the raw seed (ADR-0043 records why).
//
// The public key is rendered as a DKIM TXT record value
// `v=DKIM1; k=ed25519; p=<base64-32B-pub>` — one DNS string, no
// multi-string split (RSA-2048 would need that; Ed25519 doesn't).
package dkim

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrInvalidSeed is returned by LoadEd25519 when the on-disk key is not
// a base64-encoded 32-byte Ed25519 seed.
var ErrInvalidSeed = errors.New("dkim: key file is not a valid Ed25519 seed")

// Ed25519Key is the byte-exact contract panel-agent hands back to
// panel-api on domain.email_enable. Both fields are ASCII bytes;
// callers can Write them verbatim.
type Ed25519Key struct {
	// PrivateRawBase64 is the standard-base64 encoding of the 32-byte
	// Ed25519 seed. Write to /etc/jabali-panel/dkim/<domain>.key with
	// mode 0600.
	PrivateRawBase64 []byte

	// PublicDKIMTxt is the DNS TXT record value, ready to paste into a
	// `v=DKIM1; k=ed25519; p=...` zone entry. No surrounding quotes, no
	// trailing newline.
	PublicDKIMTxt []byte
}

// GenerateEd25519 creates a fresh Ed25519 keypair suitable for DKIM
// signing. Returns both the on-disk private form and the DNS TXT form.
//
// crypto/ed25519.GenerateKey returns a 64-byte private (seed || pub);
// we serialise just the 32-byte seed for the on-disk form because that
// is what Stalwart's loader expects and because
// ed25519.NewKeyFromSeed(seed) deterministically reproduces the full
// private key.
func GenerateEd25519() (Ed25519Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Ed25519Key{}, fmt.Errorf("dkim: ed25519.GenerateKey: %w", err)
	}

	// priv.Seed() returns the 32-byte seed; base64 without padding is a
	// common DKIM convention but plain StdEncoding (with padding) is
	// what Stalwart's loader accepts — stick with StdEncoding to avoid
	// surprising the parser with a trailing `=`.
	seed := priv.Seed()
	privB64 := make([]byte, base64.StdEncoding.EncodedLen(len(seed)))
	base64.StdEncoding.Encode(privB64, seed)

	pubB64 := base64.StdEncoding.EncodeToString(pub)
	txt := fmt.Sprintf("v=DKIM1; k=ed25519; p=%s", pubB64)

	return Ed25519Key{
		PrivateRawBase64: privB64,
		PublicDKIMTxt:    []byte(txt),
	}, nil
}

// PublicDKIMTxtFromSeed re-derives the DKIM TXT record value for an
// existing Ed25519 seed. The agent uses this after reading a key file
// back from disk — DKIM signing with a stored seed would produce a
// signature the freshly-rendered TXT wouldn't match, so the handler
// needs one canonical derivation path for both freshly-generated and
// reloaded keys.
//
// Output format is byte-identical to Ed25519Key.PublicDKIMTxt so a
// fixture captured from one path validates against the other.
func PublicDKIMTxtFromSeed(seed []byte) ([]byte, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidSeed, len(seed), ed25519.SeedSize)
	}
	pub, ok := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("ed25519.NewKeyFromSeed returned unexpected public-key type")
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	return []byte(fmt.Sprintf("v=DKIM1; k=ed25519; p=%s", pubB64)), nil
}

// SeedToPKCS8PEM wraps a 32-byte Ed25519 seed as a PEM-encoded
// PKCS#8 private key, the format Stalwart v0.16's DkimSignature
// ingestion expects (source:
// crates/common/src/config/smtp/auth.rs —
// `Ed25519Key::from_pkcs8_maybe_unchecked_der` after PEM-decoding).
//
// The on-disk form under /etc/jabali-panel/dkim/<domain>.key stays
// raw-seed base64 (ADR-0043 + the package doc-comment explain why).
// This helper is only used when preparing the JMAP payload for
// x:DkimSignature/set create, so that Stalwart receives the private
// key in the shape it wants while the panel's backup/DR path keeps
// the simplest possible format.
//
// Output ends with a trailing newline per RFC 7468; the PEM block
// type is "PRIVATE KEY" (PKCS#8, unencrypted).
func SeedToPKCS8PEM(seed []byte) ([]byte, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidSeed, len(seed), ed25519.SeedSize)
	}
	key := ed25519.NewKeyFromSeed(seed)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("dkim: marshal PKCS#8: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}), nil
}

// LoadEd25519 reads a key file written by WritePrivate and returns the
// 32-byte Ed25519 seed. Validates length; does not verify cryptographic
// invariants (any 32 random bytes is a legal seed).
func LoadEd25519(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // caller-supplied path, mode 0600 on disk
	if err != nil {
		return nil, fmt.Errorf("dkim: read %s: %w", path, err)
	}
	// Strip a single trailing newline if present (WritePrivate adds one).
	if n := len(data); n > 0 && data[n-1] == '\n' {
		data = data[:n-1]
	}
	seed, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSeed, err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidSeed, len(seed), ed25519.SeedSize)
	}
	return seed, nil
}

// WritePrivate writes the private key bytes (from Ed25519Key.PrivateRawBase64)
// to path atomically: create a temp file in the same directory, chmod it to
// 0600, then rename over the target. If the rename fails the temp file is
// removed before the error is returned, so a failed call does not leave a
// stray keyfile behind.
//
// A trailing `\n` is appended if the input does not already end in one
// (editors + cat friendliness; LoadEd25519 trims it).
func WritePrivate(path string, priv []byte) error {
	if len(priv) == 0 {
		return errors.New("dkim: empty private key")
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".dkim-*.tmp")
	if err != nil {
		return fmt.Errorf("dkim: CreateTemp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Belt-and-braces cleanup: if anything past this point fails,
	// remove the temp file before returning.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	// Chmod before write so the window where the file exists with mode
	// 0600 is as narrow as possible. (CreateTemp uses 0600 already on
	// Linux, but umask can loosen it on older Go — be explicit.)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("dkim: chmod temp: %w", err)
	}

	if _, err := tmp.Write(priv); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("dkim: write temp: %w", err)
	}
	if n := len(priv); priv[n-1] != '\n' {
		if _, err := tmp.Write([]byte{'\n'}); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("dkim: write newline: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("dkim: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("dkim: close temp: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("dkim: rename %s -> %s: %w", tmpName, path, err)
	}

	success = true
	return nil
}
