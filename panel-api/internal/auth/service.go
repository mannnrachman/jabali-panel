package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/twofa"
)

// Public sentinel errors. Callers map both to 401 — they must not leak which
// one fired, to avoid account enumeration and token-state oracles.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrInvalidToken       = errors.New("auth: invalid token")
	// ErrInvalid2FACode is returned by ChallengeTOTP on a bad TOTP or
	// backup code. Mapped to 401 by the handler; never distinguished from
	// ErrInvalidCredentials in API responses.
	ErrInvalid2FACode = errors.New("auth: invalid 2fa code")
)

// purpose2FAPending is the JWT Purpose claim value for the short-lived
// token issued between password-success and TOTP-challenge.
const purpose2FAPending = "2fa_pending"

// twoFAPendingTTL is how long the pending token stays valid. Long enough
// for a user to fumble in the app; short enough that a stolen pending
// token from a network observer is useless for sustained attack.
const twoFAPendingTTL = 5 * time.Minute

// ServiceConfig wires together the collaborators AuthService needs.
type ServiceConfig struct {
	Users       repository.UserRepository
	RefreshRepo repository.RefreshTokenRepository
	JWT         *JWTIssuer
	BcryptCost  int           // cost to use when hashing (production = bcrypt.DefaultCost)
	RefreshTTL  time.Duration // how long a refresh token stays valid

	// TOTPBackupCodes + SSOKey are optional. When nil, 2FA challenges
	// fail closed — users with totp_enabled=true would get ErrInvalid2FACode
	// for every attempt. Production wires both from serve.go.
	TOTPBackupCodes repository.TOTPBackupCodeRepository
	SSOKey          *ssokey.Key
}

// Service is the policy layer: takes credentials in, returns tokens; takes
// a refresh cookie in, returns rotated tokens. Handlers stay thin.
type Service struct {
	cfg ServiceConfig
}

// NewService returns a Service with the supplied collaborators. Panics are
// avoided: misconfiguration (nil repos, nil JWT) is the caller's problem.
func NewService(cfg ServiceConfig) *Service { return &Service{cfg: cfg} }

// LoginInput is the plain-text credential tuple from the HTTP handler.
type LoginInput struct {
	Email    string
	Password string
	DeviceID string
}

// LoginOutput holds the access JWT, the raw refresh token (cookie-bound),
// and the hydrated user the handler needs to build its response DTO.
//
// When TwoFAPending is true, AccessToken + RawRefresh are empty and
// PendingToken holds the short-lived 2fa_pending JWT. Handler must NOT
// set the refresh cookie in that case; a second POST /auth/2fa/challenge
// completes the login.
type LoginOutput struct {
	AccessToken string
	RawRefresh  string
	User        *models.User

	TwoFAPending bool
	PendingToken string
}

// Login validates email+password, issues an access JWT, creates a refresh
// token row, and returns everything the handler needs. On any failure the
// error is ErrInvalidCredentials so the response shape cannot distinguish
// "no such user" from "wrong password".
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginOutput, error) {
	u, err := s.cfg.Users.FindByEmail(ctx, in.Email)
	if err != nil {
		// Hash the DummyHash to equalise timing with the happy path.
		_ = VerifyPassword(DummyHash, in.Password)
		return nil, ErrInvalidCredentials
	}
	if !VerifyPassword(u.PasswordHash, in.Password) {
		return nil, ErrInvalidCredentials
	}

	// 2FA gate: if the user has TOTP enabled, don't issue the real tokens
	// yet. Mint a short-lived pending JWT that can only be exchanged at
	// /auth/2fa/challenge after the user proves a code.
	if u.TOTPEnabled {
		pending, err := s.cfg.JWT.IssueAccessWithTTL(AccessClaims{
			UserID:  u.ID,
			Email:   u.Email,
			IsAdmin: u.IsAdmin,
			Purpose: purpose2FAPending,
		}, twoFAPendingTTL)
		if err != nil {
			return nil, err
		}
		return &LoginOutput{
			User:         u,
			TwoFAPending: true,
			PendingToken: pending,
		}, nil
	}

	access, raw, err := s.issueAccessAndRefresh(ctx, u, in.DeviceID)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{AccessToken: access, RawRefresh: raw, User: u}, nil
}

// ChallengeTOTPInput is what /auth/2fa/challenge posts.
type ChallengeTOTPInput struct {
	// PendingToken is the Bearer token from Login with TwoFAPending=true.
	PendingToken string
	// Code is either a 6-digit TOTP or an 8-digit backup code. Exactly one
	// of the two matches the user's stored secret or unused backup row.
	Code     string
	DeviceID string
}

// ChallengeTOTP completes the second leg of a 2FA login. It verifies the
// pending JWT, then matches Code against either the user's TOTP secret or
// an unused backup code. On success it mints the real access+refresh
// tokens just like a normal Login would have.
func (s *Service) ChallengeTOTP(ctx context.Context, in ChallengeTOTPInput) (*LoginOutput, error) {
	claims, err := s.cfg.JWT.ParseAccess(in.PendingToken)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if claims.Purpose != purpose2FAPending {
		return nil, ErrInvalidToken
	}
	u, err := s.cfg.Users.FindByID(ctx, claims.UserID)
	if err != nil || u == nil || !u.TOTPEnabled || u.TOTPSecretEncrypted == nil {
		return nil, ErrInvalidToken
	}
	if s.cfg.SSOKey == nil || s.cfg.TOTPBackupCodes == nil {
		return nil, ErrInvalid2FACode
	}

	// Try TOTP first (6 digits), then fall through to backup codes.
	matched := false
	if len(in.Code) == 6 {
		secret, decErr := s.cfg.SSOKey.Open(u.TOTPSecretEncrypted)
		if decErr != nil {
			return nil, ErrInvalid2FACode
		}
		matched = twofa.Verify(string(secret), in.Code)
	} else {
		// Backup code: iterate unused rows and bcrypt-compare. Acceptable
		// cost: ≤10 rows * single-hash compare per attempt, rate-limited
		// at the handler layer.
		rows, lerr := s.cfg.TOTPBackupCodes.ListUnusedByUserID(ctx, u.ID)
		if lerr != nil {
			return nil, fmt.Errorf("list backup codes: %w", lerr)
		}
		for i := range rows {
			if twofa.MatchCode(rows[i].CodeHash, in.Code) {
				if mErr := s.cfg.TOTPBackupCodes.MarkUsed(ctx, rows[i].ID, time.Now().UTC()); mErr != nil {
					return nil, fmt.Errorf("mark backup code used: %w", mErr)
				}
				matched = true
				break
			}
		}
	}
	if !matched {
		return nil, ErrInvalid2FACode
	}

	access, raw, err := s.issueAccessAndRefresh(ctx, u, in.DeviceID)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{AccessToken: access, RawRefresh: raw, User: u}, nil
}

// RefreshInput carries the cookie value + device hint.
type RefreshInput struct {
	RawRefresh string
	DeviceID   string
}

// Refresh rotates the refresh token and returns a fresh pair. The old
// refresh row is atomically revoked inside the Rotate transaction.
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (*LoginOutput, error) {
	oldHash := HashRefreshToken(in.RawRefresh)

	existing, err := s.cfg.RefreshRepo.FindByHash(ctx, oldHash)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if existing.RevokedAt != nil || existing.ExpiresAt.Before(time.Now().UTC()) {
		return nil, ErrInvalidToken
	}

	u, err := s.cfg.Users.FindByID(ctx, existing.UserID)
	if err != nil {
		return nil, ErrInvalidToken
	}

	newRaw, newHash, err := GenerateRefreshToken()
	if err != nil {
		return nil, err
	}
	newTok := &models.RefreshToken{
		ID:        ids.NewULID(),
		UserID:    u.ID,
		DeviceID:  in.DeviceID,
		TokenHash: newHash,
		ExpiresAt: time.Now().UTC().Add(s.cfg.RefreshTTL),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.cfg.RefreshRepo.Rotate(ctx, oldHash, newTok); err != nil {
		return nil, ErrInvalidToken
	}

	claims := AccessClaims{
		UserID:  u.ID,
		Email:   u.Email,
		IsAdmin: u.IsAdmin,
	}

	access, err := s.cfg.JWT.IssueAccess(claims)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{AccessToken: access, RawRefresh: newRaw, User: u}, nil
}

// Logout best-effort revokes the refresh token tied to raw. Unknown tokens
// are not an error — a client sending a stale cookie should still see
// "logged out" without leaking whether the token existed.
func (s *Service) Logout(ctx context.Context, raw string) error {
	h := HashRefreshToken(raw)
	row, err := s.cfg.RefreshRepo.FindByHash(ctx, h)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return err
	}
	return s.cfg.RefreshRepo.Revoke(ctx, row.ID, time.Now().UTC())
}

// issueAccessAndRefresh is the common path for Login + Refresh: it produces
// a fresh access JWT and persists a new refresh-token row.
func (s *Service) issueAccessAndRefresh(ctx context.Context, u *models.User, deviceID string) (access, raw string, err error) {
	access, err = s.cfg.JWT.IssueAccess(AccessClaims{
		UserID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin,
	})
	if err != nil {
		return "", "", err
	}
	raw, hash, err := GenerateRefreshToken()
	if err != nil {
		return "", "", err
	}
	tok := &models.RefreshToken{
		ID:        ids.NewULID(),
		UserID:    u.ID,
		DeviceID:  deviceID,
		TokenHash: hash,
		ExpiresAt: time.Now().UTC().Add(s.cfg.RefreshTTL),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.cfg.RefreshRepo.Create(ctx, tok); err != nil {
		return "", "", err
	}
	return access, raw, nil
}


// RedeemCLIToken validates a break-glass CLI login token and issues fresh access/refresh tokens.
// Used by the `jabali admin-login` command (M5b). Slated for removal in M20 step 7 alongside
// the admin-login CLI itself — until then this keeps the break-glass path alive.
// The CLI token must:
// - Be valid and not expired
// - Have purpose="cli_login"
// - Reference an existing admin user
func (s *Service) RedeemCLIToken(ctx context.Context, cliToken string, deviceID string) (*LoginOutput, error) {
	claims, err := s.cfg.JWT.Verify(cliToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	// Validate purpose claim — must be "cli_login"
	if claims.Purpose != "cli_login" {
		return nil, ErrInvalidToken
	}

	// Load user and verify they still exist
	u, err := s.cfg.Users.FindByID(ctx, claims.UserID)
	if err != nil {
		return nil, ErrInvalidToken
	}

	// Break-glass admin login only — non-admin CLI tokens are rejected.
	if !u.IsAdmin {
		return nil, ErrInvalidToken
	}

	access, raw, err := s.issueAccessAndRefresh(ctx, u, deviceID)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{AccessToken: access, RawRefresh: raw, User: u}, nil
}
