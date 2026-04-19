package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newUserLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "login <email-or-id>",
		Short:   "Issue a user login URL (accepts email or user ID)",
		Long:    "Generate a one-time login URL for a user (5-minute expiry). Accepts an email address or a user ULID.",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initDB(); err != nil {
				return err
			}
			identifier := args[0]
			return runUserLogin(cmd.Context(), sharedCfg, sharedDB, sharedLog, identifier)
		},
	}

	return cmd
}

func runUserLogin(ctx context.Context, cfg *config.Config, db *gorm.DB, log *slog.Logger, identifier string) error {
	userRepo := repository.NewUserRepository(db)

	// Accept either an email (contains @) or a user ULID.
	var targetUser *models.User
	var err error
	if strings.Contains(identifier, "@") {
		targetUser, err = userRepo.FindByEmail(ctx, identifier)
	} else {
		targetUser, err = userRepo.FindByID(ctx, identifier)
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("user %q not found", identifier)
		}
		return fmt.Errorf("query user: %w", err)
	}

	if targetUser == nil {
		return fmt.Errorf("user %q not found", identifier)
	}

	// Check if user is an admin — cannot use user login for admins
	if targetUser.IsAdmin {
		return fmt.Errorf("user %q is an admin; use 'jabali-panel admin login' instead", identifier)
	}

	// Issue JWT with purpose="cli_login" and 60-second TTL
	issuer, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte(cfg.Auth.JWTSecret),
		Issuer:    "jabali-panel",
		KeyID:     jwtKeyID,
		AccessTTL: 5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("create jwt issuer: %w", err)
	}

	token, err := issuer.IssueAccessWithTTL(auth.AccessClaims{
		UserID:  targetUser.ID,
		Email:   targetUser.Email,
		IsAdmin: targetUser.IsAdmin,
		Purpose: "cli_login",
	}, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("issue token: %w", err)
	}

	// Build URL
	scheme := "http"
	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		scheme = "https"
	}

	hostname := cfg.Server.Hostname
	if hostname == "" {
		host, _, err := net.SplitHostPort(cfg.Server.Addr)
		if err != nil {
			return fmt.Errorf("parse server addr: %w", err)
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			hostname = "localhost"
		} else {
			hostname = host
		}
	}

	_, port, err := net.SplitHostPort(cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("parse server addr: %w", err)
	}

	loginURL := fmt.Sprintf("%s://%s:%s/login?cli_token=%s", scheme, hostname, port, token)

	expiresAt := time.Now().Add(5 * time.Minute).Local().Format(time.RFC3339)

	// Print to stdout (safe for piping)
	fmt.Printf("User login URL (valid for 60 seconds):\n\n  %s\n\nExpires at: %s\n", loginURL, expiresAt)

	// Audit log
	log.Info("event=audit kind=user_cli_login_issued actor_id=cli target_id=" + targetUser.ID)

	return nil
}
