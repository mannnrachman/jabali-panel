package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
)

// JWTConfig describes how the issuer signs and validates access tokens.
type JWTConfig struct {
	// Secret is the HMAC-SHA256 key. At least 32 bytes.
	Secret []byte
	// Issuer populates the "iss" claim (e.g. "jabali-panel").
	Issuer string
	// KeyID is emitted in the header as "kid". Allows future key rotation
	// without invalidating every live token.
	KeyID string
	// AccessTTL is how long an issued access token stays valid.
	AccessTTL time.Duration
}

// AccessClaims is the application-level view of an access token's payload.
// It wraps jwt.RegisteredClaims so iss/exp/iat/jti are available too.
type AccessClaims struct {
	UserID        string `json:"sub"`
	Email         string `json:"email"`
	IsAdmin       bool   `json:"admin"`
	Purpose       string `json:"purpose,omitempty"` // e.g., "cli_login" for break-glass tokens
	ImpersonatedBy string `json:"impersonated_by,omitempty"` // set when token was issued via impersonation
	jwt.RegisteredClaims
}

// JWTIssuer issues and verifies access tokens for a single active key.
// Multiple issuers with different KeyIDs can coexist during rotation if the
// caller wires them that way; verifying beyond a single kid is out of scope
// for Phase 4 and will live in a key-rotation helper later.
type JWTIssuer struct {
	cfg JWTConfig
}

const (
	minSecretLen = 32
	signingAlg   = "HS256"
)

// NewJWTIssuer validates cfg and returns an issuer ready for IssueAccess.
func NewJWTIssuer(cfg JWTConfig) (*JWTIssuer, error) {
	if len(cfg.Secret) < minSecretLen {
		return nil, fmt.Errorf("auth: JWT secret must be >= %d bytes, got %d",
			minSecretLen, len(cfg.Secret))
	}
	if cfg.Issuer == "" {
		return nil, errors.New("auth: JWT issuer cannot be empty")
	}
	if cfg.KeyID == "" {
		return nil, errors.New("auth: JWT key id cannot be empty")
	}
	return &JWTIssuer{cfg: cfg}, nil
}

// IssueAccess returns a signed access JWT with iss/exp/iat/jti populated.
// Caller provides UserID + Email + IsAdmin; everything else is derived here.
func (i *JWTIssuer) IssueAccess(c AccessClaims) (string, error) {
	return i.issueAccessWithTTL(c, i.cfg.AccessTTL)
}

// IssueAccessWithTTL is like IssueAccess but allows customizing the token's TTL.
func (i *JWTIssuer) IssueAccessWithTTL(c AccessClaims, ttl time.Duration) (string, error) {
	return i.issueAccessWithTTL(c, ttl)
}

func (i *JWTIssuer) issueAccessWithTTL(c AccessClaims, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	c.RegisteredClaims = jwt.RegisteredClaims{
		Issuer:    i.cfg.Issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now.Add(-time.Second)), // clock skew grace
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		ID:        ids.NewULID(),
		Subject:   c.UserID,
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	tok.Header["kid"] = i.cfg.KeyID
	return tok.SignedString(i.cfg.Secret)
}

// Verify parses s, checks signature/alg/kid/exp, and returns the claims.
// An error from Verify MUST be treated as authentication failure; callers
// must not branch on the error type — just 401.
func (i *JWTIssuer) Verify(s string) (*AccessClaims, error) {
	var out AccessClaims
	parsed, err := jwt.ParseWithClaims(s, &out, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != signingAlg {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		kid, _ := t.Header["kid"].(string)
		if kid != i.cfg.KeyID {
			return nil, fmt.Errorf("unknown kid %q", kid)
		}
		return i.cfg.Secret, nil
	},
		jwt.WithIssuer(i.cfg.Issuer),
		jwt.WithValidMethods([]string{signingAlg}),
	)
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, errors.New("auth: invalid token")
	}
	return &out, nil
}

// ParseAccess is an alias to Verify for backwards compatibility with tests.
func (i *JWTIssuer) ParseAccess(s string) (*AccessClaims, error) {
	return i.Verify(s)
}
