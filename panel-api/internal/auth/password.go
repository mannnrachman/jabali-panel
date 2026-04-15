// Package auth holds password hashing, JWT issue/verify, refresh-token
// generation, and the AuthService that composes them into login/refresh/logout.
//
// The package intentionally keeps no request/DB state — it is a library of
// small, testable building blocks used by HTTP handlers.
package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// DummyHash is a bcrypt hash of a value no caller will ever pass. The
// AuthService calls VerifyPassword against this when the supplied email is
// unknown, so the response time of "user not found" matches "wrong password"
// and attackers can't enumerate existing emails by stopwatch.
//
// Generated once at cost=12 and checked in verbatim so the cost matches
// production. If you bump the production cost, regenerate this with the
// new cost.
//
//	go run -tags dummy ./cmd/gen-dummy-hash
const DummyHash = "$2a$12$QRgLq9jlRqLmNbN5i0mA.uTbg5LFXYMa/5Zj3KtZKzF.QnZ5oEu2m"

// HashPassword returns a bcrypt hash of plain at the given cost. Cost is
// typically bcrypt.DefaultCost in production (currently 12); tests pass a
// low cost to keep the test suite fast.
func HashPassword(plain string, cost int) (string, error) {
	if plain == "" {
		return "", errors.New("auth: password cannot be empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword returns true iff plain matches the stored bcrypt hash.
// Safe to call with a malformed hash (returns false, no panic), which lets
// the AuthService use DummyHash unconditionally to equalise timing.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
