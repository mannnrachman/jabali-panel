package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Public sentinel errors. Callers map both to 401 — they must not leak which
// one fired, to avoid account enumeration and token-state oracles.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrInvalidToken       = errors.New("auth: invalid token")
)

// ServiceConfig wires together the collaborators AuthService needs.
type ServiceConfig struct {
	Users       repository.UserRepository
	RefreshRepo repository.RefreshTokenRepository
	JWT         *JWTIssuer
	BcryptCost  int           // cost to use when hashing (production = bcrypt.DefaultCost)
	RefreshTTL  time.Duration // how long a refresh token stays valid
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
type LoginOutput struct {
	AccessToken string
	RawRefresh  string
	User        *models.User
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
		ID:             ids.NewULID(),
		UserID:         u.ID,
		DeviceID:       in.DeviceID,
		TokenHash:      newHash,
		ExpiresAt:      time.Now().UTC().Add(s.cfg.RefreshTTL),
		ImpersonatedBy: existing.ImpersonatedBy,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.cfg.RefreshRepo.Rotate(ctx, oldHash, newTok); err != nil {
		return nil, ErrInvalidToken
	}

	// Preserve ImpersonatedBy if the session was impersonated
	claims := AccessClaims{
		UserID:   u.ID,
		Email:    u.Email,
		IsAdmin:  u.IsAdmin,
	}
	if existing.ImpersonatedBy != nil {
		claims.ImpersonatedBy = *existing.ImpersonatedBy
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
// The CLI token must:
// - Be valid and not expired
// - Have purpose="cli_login"
// - Reference an existing user
//
// If the token has impersonated_by set, it indicates an admin impersonation:
// - Issue only an access token with 1h TTL, no refresh token (one-shot tab)
//
// If impersonated_by is empty, it indicates a break-glass admin login:
// - Issue access + refresh tokens via the normal path (persistent session)
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

	// If impersonated_by is set, this is an impersonation token (one-shot, admin-initiated)
	// Issue only access token, no refresh token
	if claims.ImpersonatedBy != "" {
		// Issue access token with 1h TTL, preserve impersonated_by claim
		access, err := s.cfg.JWT.IssueAccessWithTTL(AccessClaims{
			UserID:        u.ID,
			Email:         u.Email,
			IsAdmin:       u.IsAdmin,
			ImpersonatedBy: claims.ImpersonatedBy,
		}, 1*time.Hour)
		if err != nil {
			return nil, err
		}
		// RawRefresh is empty to signal no refresh cookie should be set
		return &LoginOutput{AccessToken: access, RawRefresh: "", User: u}, nil
	}

	// Break-glass admin login: issue fresh access + refresh tokens via the normal path
	// (implies the token was issued for a regular admin user, not impersonation)
	if !u.IsAdmin {
		return nil, ErrInvalidToken
	}

	access, raw, err := s.issueAccessAndRefresh(ctx, u, deviceID)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{AccessToken: access, RawRefresh: raw, User: u}, nil
}
// ImpersonationOutput holds the tokens returned by IssueImpersonation.
type ImpersonationOutput struct {
	AccessToken string
	RawRefresh  string
}

// IssueImpersonation creates an access token with the ImpersonatedBy claim set
// to the adminID, along with a new refresh token. This is used when an admin
// initiates user impersonation.
func (s *Service) IssueImpersonation(ctx context.Context, targetUser *models.User, adminID string) (*ImpersonationOutput, error) {
	// Generate refresh token and save it
	raw, hash, err := GenerateRefreshToken()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	tok := &models.RefreshToken{
		ID:             ids.NewULID(),
		UserID:         targetUser.ID,
		TokenHash:      hash,
		DeviceID:       "", // No device hint for impersonation
		ExpiresAt:      now.Add(s.cfg.RefreshTTL),
		ImpersonatedBy: &adminID,
		CreatedAt:      now,
	}
	if err := s.cfg.RefreshRepo.Create(ctx, tok); err != nil {
		return nil, err
	}

	// Issue access token with ImpersonatedBy claim
	claims := AccessClaims{
		UserID:        targetUser.ID,
		Email:         targetUser.Email,
		IsAdmin:       targetUser.IsAdmin,
		ImpersonatedBy: adminID,
	}
	access, err := s.cfg.JWT.IssueAccess(claims)
	if err != nil {
		return nil, err
	}

	return &ImpersonationOutput{AccessToken: access, RawRefresh: raw}, nil
}

// GenerateImpersonationLoginURL creates a one-time login URL for admin impersonation.
// The URL contains a 60-second cli_login JWT with impersonated_by set to adminID.
// The URL does not create a persistent refresh token — the impersonated session is 1h-only.
func (s *Service) GenerateImpersonationLoginURL(
	ctx context.Context,
	targetUser *models.User,
	adminID string,
	scheme string,
	hostname string,
	port string,
) (string, error) {
	// Issue JWT with purpose="cli_login", impersonated_by=adminID, 60-second TTL
	claims := AccessClaims{
		UserID:        targetUser.ID,
		Email:         targetUser.Email,
		IsAdmin:       targetUser.IsAdmin,
		ImpersonatedBy: adminID,
		Purpose:       "cli_login",
	}
	token, err := s.cfg.JWT.IssueAccessWithTTL(claims, 60*time.Second)
	if err != nil {
		return "", err
	}

	// Build login URL
	loginURL := fmt.Sprintf("%s://%s:%s/login?cli_token=%s", scheme, hostname, port, token)
	return loginURL, nil
}
