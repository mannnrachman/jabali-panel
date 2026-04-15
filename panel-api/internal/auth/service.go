package auth

import (
	"context"
	"errors"
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

	access, err := s.cfg.JWT.IssueAccess(AccessClaims{
		UserID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin,
	})
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
