package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

const testSecret = "test-secret-must-be-at-least-32-bytes-long"

func newIssuer(t *testing.T) *auth.JWTIssuer {
	t.Helper()
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte(testSecret),
		Issuer:    "jabali-panel-test",
		KeyID:     "v1",
		AccessTTL: 15 * time.Minute,
	})
	require.NoError(t, err)
	return iss
}

func TestIssueAccessToken_ShapeAndRoundTrip(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t)

	tok, err := iss.IssueAccess(auth.AccessClaims{
		UserID:  "01HRCWR7CKMCBEDF2PYQ7G0D2J",
		Email:   "alice@example.com",
		IsAdmin: true,
	})
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3, "JWT must have header.payload.signature")

	// Verify round-trip: parsing the issued token yields the same claims.
	got, err := iss.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, "01HRCWR7CKMCBEDF2PYQ7G0D2J", got.UserID)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.True(t, got.IsAdmin)
	assert.Equal(t, "jabali-panel-test", got.Issuer)
	assert.NotEmpty(t, got.ID, "jti must be present")
}

func TestIssueAccessToken_HeaderHasKeyID(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t)

	tok, err := iss.IssueAccess(auth.AccessClaims{UserID: "u", Email: "e@x"})
	require.NoError(t, err)

	// Parse without verifying so we can inspect the header.
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	require.NoError(t, err)
	assert.Equal(t, "v1", parsed.Header["kid"])
	assert.Equal(t, "HS256", parsed.Header["alg"])
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t)

	tok, err := iss.IssueAccess(auth.AccessClaims{UserID: "u", Email: "e@x"})
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	// Swap the payload for a different base64 blob; the signature will no longer verify.
	tampered := parts[0] + "." + "eyJmYWtlIjoidmFsdWUifQ" + "." + parts[2]

	_, err = iss.Verify(tampered)
	require.Error(t, err)
}

func TestVerify_RejectsWrongAlg(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t)

	// Build a none-alg token by hand (attacker attempt).
	noneTok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: "attacker",
	})
	bad, err := noneTok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = iss.Verify(bad)
	require.Error(t, err, "tokens without HS256 signature must be rejected")
}

func TestVerify_RejectsExpired(t *testing.T) {
	t.Parallel()
	iss, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte(testSecret),
		Issuer:    "t",
		KeyID:     "v1",
		AccessTTL: -1 * time.Hour, // already expired on issue
	})
	require.NoError(t, err)

	tok, err := iss.IssueAccess(auth.AccessClaims{UserID: "u", Email: "e@x"})
	require.NoError(t, err)

	_, err = iss.Verify(tok)
	require.Error(t, err)
}

func TestVerify_RejectsUnknownKeyID(t *testing.T) {
	t.Parallel()
	iss := newIssuer(t)
	// Hand-craft a token with an unknown kid to simulate a token issued by a
	// rotated-out key. Verify should refuse.
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   "u",
		"email": "e@x",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"iss":   "jabali-panel-test",
	})
	tok.Header["kid"] = "v-unknown"
	signed, err := tok.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = iss.Verify(signed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kid")
}

func TestNewJWTIssuer_RejectsShortSecret(t *testing.T) {
	t.Parallel()
	_, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte("too-short"),
		Issuer:    "t",
		KeyID:     "v1",
		AccessTTL: time.Minute,
	})
	require.Error(t, err)
}
