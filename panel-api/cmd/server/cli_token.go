package main

import (
	"context"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
)

// mintCLIToken mints a short-lived admin JWT token for CLI authentication.
func mintCLIToken(ctx context.Context) (string, error) {
	if sharedCfg == nil {
		return "", fmt.Errorf("mintCLIToken: config not loaded")
	}

	// Must match the server's issuer/kid (see cmd/server/serve.go) so the
	// API verifier accepts our token. Short TTL because this is a CLI
	// invocation — token only needs to live long enough for a single HTTP
	// call.
	jwtCfg := auth.JWTConfig{
		Secret:    []byte(sharedCfg.Auth.JWTSecret),
		Issuer:    jwtIssuerName,
		KeyID:     jwtKeyID,
		AccessTTL: 2 * time.Minute,
	}

	issuer, err := auth.NewJWTIssuer(jwtCfg)
	if err != nil {
		return "", fmt.Errorf("create JWT issuer: %w", err)
	}

	// Synthetic admin identity used only for local-root CLI calls. The
	// UserID doesn't need to exist as a DB row — middleware only checks
	// signature + IsAdmin for admin-only endpoints.
	claims := auth.AccessClaims{
		UserID:  "cli-root",
		Email:   "cli@jabali.local",
		IsAdmin: true,
	}

	token, err := issuer.IssueAccess(claims)
	if err != nil {
		return "", fmt.Errorf("issue JWT: %w", err)
	}

	return token, nil
}
