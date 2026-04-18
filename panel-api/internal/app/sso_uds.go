package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ssokey"
)

// StartSSOUDSListener starts a Unix domain socket HTTP server for phpMyAdmin SSO validation.
// The listener binds to socketPath with mode 0660 and owner jabali:www-data.
// It handles POST /sso/phpmyadmin/validate requests without JWT auth; the socket ACL is the boundary.
// The server runs in the background; callers must call the returned cancel func to gracefully shutdown.
// Stale socket files are removed before binding.
func StartSSOUDSListener(
	socketPath string,
	databases repository.DatabaseRepository,
	users repository.UserRepository,
	tokens repository.PhpMyAdminSSOTokenRepository,
	ssoKey *ssokey.Key,
	log *slog.Logger,
) (*http.Server, func(context.Context) error, error) {
	if socketPath == "" {
		return nil, nil, errors.New("socketPath is required")
	}

	// Remove stale socket file if it exists
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("remove stale socket: %w", err)
	}

	// Create the router
	r := gin.New()
	r.Use(gin.Recovery())

	cfg := api.SSOPhpMyAdminValidateHandlerConfig{
		Databases: databases,
		Users:     users,
		Tokens:    tokens,
		SSOKey:    ssoKey,
		Log:       log,
	}
	g := r.Group("")
	api.RegisterSSOPhpMyAdminValidateRoutes(g, cfg)

	// Create Unix domain socket listener
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on unix socket: %w", err)
	}

	// Set socket file permissions: 0660, owner jabali:www-data
	// Note: In production, this should be adjusted based on actual user/group IDs.
	// For now, we set mode 0660 and trust the deployment to set ownership via systemd or manual chown.
	if err := os.Chmod(socketPath, 0660); err != nil {
		listener.Close()
		return nil, nil, fmt.Errorf("chmod socket: %w", err)
	}

	// Create HTTP server
	srv := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// Start server in background
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(listener)
	}()

	// Monitor for serve errors
	go func() {
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("SSO UDS listener error", "err", err)
			}
		}
	}()

	log.Info("SSO UDS listener started", "socket", socketPath)

	// Return server and a shutdown function
	shutdown := func(shutdownCtx context.Context) error {
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shutdown UDS server: %w", err)
		}
		// Remove socket file on shutdown
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("remove socket on shutdown", "err", err)
		}
		log.Info("SSO UDS listener stopped")
		return nil
	}

	return srv, shutdown, nil
}
