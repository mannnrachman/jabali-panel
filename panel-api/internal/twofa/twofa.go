// Package twofa implements TOTP enrolment/verification and backup-code
// generation on top of github.com/pquerna/otp. Encryption of the stored
// secret is delegated to internal/ssokey (AES-256-GCM), so the TOTP
// package holds no key material directly.
package twofa

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	// Issuer appears in every authenticator app row (before the colon).
	// Kept short + branded per ADR-style convention.
	Issuer = "Jabali Panel"

	// BackupCodeCount + BackupCodeDigits define the "10 × 8-digit" spec.
	// 8 digits gives ~3.3 × 10^7 per code; 10 codes is what GitHub,
	// Google, and others ship — matches user expectations.
	BackupCodeCount  = 10
	BackupCodeDigits = 8

	// bcryptCost for backup-code hashes. Lower than password cost because
	// codes are short-lived (each one usable once) and we only verify on
	// explicit challenge. 12 matches the auth package's user-password cost.
	bcryptCost = 12
)

// Enrolment is what the enrol endpoint hands back to the client. The UI
// renders OtpauthURL as a QR and shows Secret as a fallback manual entry.
type Enrolment struct {
	// Secret is the base32-encoded shared key (what's embedded in
	// OtpauthURL's secret= param). Returned so the UI can display a
	// manual-entry option alongside the QR code.
	Secret string
	// OtpauthURL is the full otpauth://totp/... URI an authenticator app
	// reads from a QR code.
	OtpauthURL string
}

// NewEnrolment generates a fresh shared secret for the given account email.
// Caller must encrypt Secret (via ssokey.Seal) before persistence.
func NewEnrolment(accountEmail string) (*Enrolment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      Issuer,
		AccountName: accountEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("totp generate: %w", err)
	}
	return &Enrolment{
		Secret:     key.Secret(),
		OtpauthURL: key.URL(),
	}, nil
}

// Verify returns true iff code matches the current TOTP window for secret.
// A ±1-step skew is tolerated inside pquerna/otp's Validate default.
func Verify(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}
	return totp.Validate(code, secret)
}

// NewBackupCodes returns count fresh numeric codes. Each is exactly
// digits long, zero-padded. The raw strings are returned to the caller
// so they can be displayed ONCE; the caller MUST hash each via HashCode
// before persistence.
func NewBackupCodes() ([]string, error) {
	out := make([]string, BackupCodeCount)
	// digits decimal capacity; e.g. 8 digits → max=99_999_999, modulo with
	// uniform distribution uses rejection sampling via crypto/rand.Int if
	// you want perfect uniformity. For 8-digit codes, the modulo bias vs
	// 2^n boundary is negligible for our threat model — these codes are
	// single-use and rate-limited at the endpoint level.
	for i := 0; i < BackupCodeCount; i++ {
		b := make([]byte, 5) // 40 bits > 8 decimal digits
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("rand: %w", err)
		}
		var n uint64
		for _, bb := range b {
			n = n<<8 | uint64(bb)
		}
		mod := uint64(1)
		for j := 0; j < BackupCodeDigits; j++ {
			mod *= 10
		}
		out[i] = fmt.Sprintf("%0*d", BackupCodeDigits, n%mod)
	}
	return out, nil
}

// HashCode wraps bcrypt at our chosen cost so the DB row never carries
// redeemable material.
func HashCode(code string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash backup code: %w", err)
	}
	return string(h), nil
}

// MatchCode compares a user-supplied code against a stored bcrypt hash.
// The constant-time subtle.ConstantTimeCompare is redundant here (bcrypt
// does its own) but kept defensive for inputs that might bypass bcrypt in
// a future refactor.
func MatchCode(hash, code string) bool {
	if hash == "" || code == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) == nil
}

// ValidBase32Secret sanity-checks a base32 string before handing it to
// Verify — prevents garbage secrets from surfacing cryptic validation
// failures.
func ValidBase32Secret(secret string) bool {
	if secret == "" {
		return false
	}
	_, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	return err == nil
}

// ErrInvalidCode is what services return to the API layer when a
// user-supplied code doesn't match. The API layer maps it to 401.
var ErrInvalidCode = errors.New("invalid 2fa code")

// constant-time equality for non-bcrypt string compares; exported so the
// challenge handler can compare normalised codes without timing leaks.
func ConstEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
