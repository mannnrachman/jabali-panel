package sshkeys

import (
	"crypto/rsa"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

const MinRSAKeyBits = 2048

var (
	ErrInvalidKeyFormat = errors.New("invalid key format")
	ErrRSATooWeak       = errors.New("RSA key below 2048 bits")
	ErrUnsupportedType  = errors.New("unsupported key type")
)

// ParseAndFingerprint validates an authorized-keys-format public key,
// returning the normalized key string and SHA256 fingerprint. Rejects
// RSA keys under 2048 bits. Supports rsa, ed25519, ecdsa-sha2-*.
func ParseAndFingerprint(raw string) (normalizedKey, fingerprint string, err error) {
	raw = strings.TrimSpace(raw)
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(raw))
	if err != nil {
		return "", "", ErrInvalidKeyFormat
	}

	// Type check + RSA bit length
	switch key.Type() {
	case ssh.KeyAlgoRSA:
		cryptoKey, ok := key.(ssh.CryptoPublicKey)
		if !ok {
			return "", "", ErrInvalidKeyFormat
		}
		rsaKey, ok := cryptoKey.CryptoPublicKey().(*rsa.PublicKey)
		if !ok {
			return "", "", ErrInvalidKeyFormat
		}
		if rsaKey.N.BitLen() < MinRSAKeyBits {
			return "", "", ErrRSATooWeak
		}
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		// Fine.
	default:
		return "", "", ErrUnsupportedType
	}

	return string(ssh.MarshalAuthorizedKey(key)), ssh.FingerprintSHA256(key), nil
}
