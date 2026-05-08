// Adminer SSO bridge — engine-aware mint + PG shadow provisioning.
//
// Mirror of the phpMyAdmin Service plumbing in sso.go but parameterised
// on engine. Public surface:
//
//   - EnsurePgShadow(userID): provisions a PostgreSQL ROLE for the
//     panel user via the agent; encrypts and persists the password
//     onto users.pgadmin_password_enc. Idempotent.
//   - MintAdminerToken(userID, databaseID, engine): inserts a row into
//     adminer_sso_tokens and returns the plaintext base64url token.
//
// The Adminer jabali-sso plugin (install/adminer/jabali-sso-plugin.php)
// reads the token from `?token=…` and POSTs it to /sso/adminer/validate
// over the panel-api UDS — the engine column lets the validate handler
// return the right driver + credentials in a single round-trip.
package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// AdminerService bundles the PG-shadow + adminer-token surface. It
// intentionally re-uses the existing Service for its agent + ssoKey
// + db handles so config/wiring stays in one place.
type AdminerService struct {
	base   *Service
	tokens repository.AdminerSSOTokenRepository
}

// NewAdminerService constructs the bridge. base must already be a
// fully-initialised phpMyAdmin sso.Service — that's where the agent +
// ssoKey + log + tokenTTL come from.
func NewAdminerService(base *Service, tokens repository.AdminerSSOTokenRepository) *AdminerService {
	return &AdminerService{base: base, tokens: tokens}
}

// EnsurePgShadow provisions a PostgreSQL ROLE for the given panel
// user and persists the AES-GCM-encrypted password onto users.
// pgadmin_password_enc. If the shadow account already exists the call
// is a no-op (returns nil).
//
// FOR UPDATE locking on the users row prevents two concurrent SSO
// click-throughs from minting two roles for the same user.
func (s *AdminerService) EnsurePgShadow(ctx context.Context, userID string) error {
	user, err := s.base.users.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("find user: %w", err)
	}
	if user.Username == nil || *user.Username == "" {
		return errors.New("user has no username")
	}

	return s.base.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&current, "id = ?", userID).Error; err != nil {
			return fmt.Errorf("select for update: %w", err)
		}
		if current.PgadminUsername != nil {
			if current.PgadminPasswordEnc == nil {
				return errors.New("pg shadow account exists but password is null")
			}
			return nil
		}

		// Agent provisions a PG ROLE LOGIN with CREATEDB on owned dbs.
		resp, err := s.base.agent.Call(ctx, "db.postgres.shadowadmin.ensure",
			map[string]interface{}{"panel_username": user.Username})
		if err != nil {
			return fmt.Errorf("agent db.postgres.shadowadmin.ensure: %w", err)
		}
		var agentResp map[string]interface{}
		if err := json.Unmarshal(resp, &agentResp); err != nil {
			return fmt.Errorf("unmarshal agent response: %w", err)
		}
		pgUsername, ok := agentResp["pgadmin_username"].(string)
		if !ok || pgUsername == "" {
			return errors.New("agent response missing pgadmin_username")
		}
		pgPassword, ok := agentResp["pgadmin_password"].(string)
		if !ok || pgPassword == "" {
			return errors.New("agent response missing pgadmin_password")
		}
		encrypted, err := s.base.ssoKey.Seal([]byte(pgPassword))
		if err != nil {
			return fmt.Errorf("encrypt password: %w", err)
		}

		now := time.Now()
		updates := models.User{
			ID:                   userID,
			PgadminUsername:      &pgUsername,
			PgadminPasswordEnc:   encrypted,
			PgadminProvisionedAt: &now,
		}
		return tx.Model(&models.User{}).
			Where("id = ?", userID).
			Updates(updates).Error
	})
}

// MintAdminerToken issues a fresh single-use token bound to the
// (user, database, engine) triple. Returned token is the plaintext
// base64url-encoded random — only its SHA-256 lands in the DB.
func (s *AdminerService) MintAdminerToken(ctx context.Context, userID, databaseID, engine string) (string, error) {
	if engine != "mariadb" && engine != "postgres" {
		return "", fmt.Errorf("invalid engine %q", engine)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(tokenBytes)
	hash := sha256.Sum256(tokenBytes)
	hashStr := fmt.Sprintf("%x", hash[:])

	row := &models.AdminerSSOToken{
		ID:         ids.NewULID(),
		UserID:     userID,
		DatabaseID: databaseID,
		Engine:     engine,
		TokenHash:  hashStr,
		ExpiresAt:  time.Now().Add(s.base.tokenTTL),
		CreatedAt:  time.Now(),
	}
	if err := s.tokens.Create(ctx, row); err != nil {
		return "", fmt.Errorf("insert adminer sso token: %w", err)
	}
	s.base.log.DebugContext(ctx, "minted adminer SSO token",
		"user_id", userID, "db_id", databaseID, "engine", engine)
	return plaintext, nil
}

// DecryptShadowPassword returns the plaintext shadow password for the
// given engine. Used by the validate handler to assemble the
// credentials response that Adminer's jabali-sso plugin forwards into
// the libpq/mysqli connect call.
func (s *AdminerService) DecryptShadowPassword(user *models.User, engine string) (string, error) {
	switch engine {
	case "mariadb":
		if user.MysqladminPasswordEnc == nil {
			return "", errors.New("mariadb shadow not provisioned")
		}
		pt, err := s.base.ssoKey.Open(user.MysqladminPasswordEnc)
		if err != nil {
			return "", fmt.Errorf("open mariadb password: %w", err)
		}
		return string(pt), nil
	case "postgres":
		if user.PgadminPasswordEnc == nil {
			return "", errors.New("postgres shadow not provisioned")
		}
		pt, err := s.base.ssoKey.Open(user.PgadminPasswordEnc)
		if err != nil {
			return "", fmt.Errorf("open postgres password: %w", err)
		}
		return string(pt), nil
	default:
		return "", fmt.Errorf("invalid engine %q", engine)
	}
}
