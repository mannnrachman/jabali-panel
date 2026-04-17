package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/auth"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/config"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func newAdminCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "admin",
		Short: "Admin commands",
	}
}

func newAdminLoginCmd() *cobra.Command {
	var email string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Issue a break-glass admin login URL",
		Long:  "Generate a one-time login URL for admin break-glass access (15-min expiry).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := initConfig(); err != nil {
				return err
			}
			if err := initDB(); err != nil {
				return err
			}

			return runAdminLogin(cmd.Context(), sharedCfg, sharedDB, sharedLog, email)
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "admin email (required if more than one admin exists)")

	return cmd
}

func runAdminLogin(ctx context.Context, cfg *config.Config, db *gorm.DB, log *slog.Logger, email string) error {
	userRepo := repository.NewUserRepository(db)

	// Count admin users
	admins, err := userRepo.FindAdminsByEmail(ctx)
	if err != nil {
		return fmt.Errorf("query admins: %w", err)
	}

	if len(admins) == 0 {
		fmt.Fprint(os.Stderr, "no admin users exist — run install.sh to bootstrap one\n")
		return fmt.Errorf("no admin users found")
	}

	var targetAdmin *models.User

	if len(admins) == 1 {
		targetAdmin = admins[0]
	} else {
		if email == "" {
			fmt.Fprint(os.Stderr, "multiple admins exist, please specify --email:\n")
			for _, admin := range admins {
				fmt.Fprintf(os.Stderr, "  %s\n", admin.Email)
			}
			return fmt.Errorf("--email required")
		}

		for _, admin := range admins {
			if admin.Email == email {
				targetAdmin = admin
				break
			}
		}
		if targetAdmin == nil {
			return fmt.Errorf("admin %q not found", email)
		}
	}

	// Issue JWT with purpose="cli_login" and 15-minute TTL
	issuer, err := auth.NewJWTIssuer(auth.JWTConfig{
		Secret:    []byte(cfg.Auth.JWTSecret),
		Issuer:    "jabali-panel",
		KeyID:     jwtKeyID,
		AccessTTL: 15 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("create jwt issuer: %w", err)
	}

	token, err := issuer.IssueAccessWithTTL(auth.AccessClaims{
		UserID:  targetAdmin.ID,
		Email:   targetAdmin.Email,
		IsAdmin: targetAdmin.IsAdmin,
		Purpose: "cli_login",
	}, 15*time.Minute)
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

	expiresAt := time.Now().Add(15 * time.Minute).Local().Format(time.RFC3339)

	// Print to stdout (safe for piping)
	fmt.Printf("Admin login URL (valid for 15 minutes):\n\n  %s\n\nExpires at: %s\n", loginURL, expiresAt)

	// Audit log
	log.Info("event=audit kind=admin_cli_token_issued actor_id=cli target_id=" + targetAdmin.ID)

	return nil
}
