package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newUserLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "login <email>",
		Short:   "Issue a user login URL",
		Long:    "Generate a one-time login URL for a user (60-second expiry).",
		Args:    cobra.ExactArgs(1),
		PreRunE: requireConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initDB(); err != nil {
				return err
			}
			email := args[0]
			return runUserLogin(cmd.Context(), sharedCfg, sharedDB, sharedLog, email)
		},
	}

	return cmd
}

func runUserLogin(ctx context.Context, cfg *config.Config, db *gorm.DB, log *slog.Logger, email string) error {
	userRepo := repository.NewUserRepository(db)

	// Look up user by email
	targetUser, err := userRepo.FindByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("query user: %w", err)
	}

	if targetUser == nil {
		return fmt.Errorf("user %q not found", email)
	}

	// Check if user is an admin — cannot use user login for admins
	if targetUser.IsAdmin {
		return fmt.Errorf("user %q is an admin; use 'jabali admin login' instead", email)
	}

	// Issue JWT with purpose="cli_login", impersonated_by="cli", and 60-second TTL
	issuer, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte(cfg.Auth.JWTSecret),
		Issuer:    "jabali-panel",
		KeyID:     "default",
		AccessTTL: 60 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create jwt issuer: %w", err)
	}

	token, err := issuer.IssueAccessWithTTL(auth.AccessClaims{
		UserID:         targetUser.ID,
		Email:          targetUser.Email,
		IsAdmin:        targetUser.IsAdmin,
		ImpersonatedBy: "cli",
		Purpose:        "cli_login",
	}, 60*time.Second)
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

	expiresAt := time.Now().Add(60 * time.Second).Local().Format(time.RFC3339)

	// Print to stdout (safe for piping)
	fmt.Printf("User login URL (valid for 60 seconds):\n\n  %s\n\nExpires at: %s\n", loginURL, expiresAt)

	// Audit log
	log.Info("event=audit kind=user_cli_login_issued actor_id=cli target_id=" + targetUser.ID)

	return nil
}
