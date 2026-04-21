package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/api"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// buildAppDeps assembles ApplicationHandlerConfig for the CLI. The
// service requires every repo + the agent + the apps registry; we
// already have sharedDB/sharedAgent from the require* helpers in
// root.go. The apps registry is built fresh per call (a few µs) to
// avoid plumbing a long-lived process-wide registry into the CLI.
func buildAppDeps() (api.ApplicationHandlerConfig, error) {
	if sharedDB == nil || sharedAgent == nil {
		return api.ApplicationHandlerConfig{}, errors.New("DB or agent not initialised (call requireDBAndAgent first)")
	}
	registry := apps.New()
	if err := apps.RegisterDefaults(registry); err != nil {
		return api.ApplicationHandlerConfig{}, fmt.Errorf("register app defaults: %w", err)
	}
	panelHost := ""
	if sharedCfg != nil {
		panelHost = sharedCfg.Server.Hostname
	}
	return api.ApplicationHandlerConfig{
		ApplicationInstalls: repository.NewApplicationInstallRepository(sharedDB),
		Databases:           repository.NewDatabaseRepository(sharedDB),
		DatabaseUsers:       repository.NewDatabaseUserRepository(sharedDB),
		DatabaseGrants:      repository.NewDatabaseUserGrantRepository(sharedDB),
		Domains:             repository.NewDomainRepository(sharedDB),
		Users:               userRepo(),
		Packages:  repository.NewPackageRepository(sharedDB),
		Agent:     sharedAgent,
		Apps:      registry,
		PanelHost: panelHost,
	}, nil
}

// installAppDirect resolves the domain owner so the operator never
// has to pass --user-id, then calls the shared service. The CLI is
// always operator-as-root so IsAdminCall is true (matches the HTTP
// handler's admin path: ownership mismatch returns "forbidden" not
// "domain_not_found").
func installAppDirect(ctx context.Context, p api.InstallParams) (*api.InstallResult, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	if err := initAgent(); err != nil {
		return nil, err
	}

	deps, err := buildAppDeps()
	if err != nil {
		return nil, err
	}

	// Operator didn't provide --user-id: fill from domain owner so the
	// install is associated with the right tenant.
	if p.UserID == "" {
		domain, dErr := repository.NewDomainRepository(sharedDB).FindByID(ctx, p.DomainID)
		if dErr != nil {
			if errors.Is(dErr, repository.ErrNotFound) {
				return nil, fmt.Errorf("domain %q not found", p.DomainID)
			}
			return nil, fmt.Errorf("lookup domain: %w", dErr)
		}
		p.UserID = domain.UserID
	}
	p.IsAdminCall = true

	res, ierr := api.InstallApplication(ctx, deps, p)
	if ierr != nil {
		return nil, ierr
	}
	return res, nil
}

// pollInstallStatus blocks until the install row reaches a terminal
// state (ready/failed/error) or the deadline fires. Returns the final
// row regardless so the caller can surface LastError.
func pollInstallStatus(parent context.Context, installID string, timeout time.Duration) (*models.ApplicationInstall, error) {
	if err := initDB(); err != nil {
		return nil, err
	}
	installs := repository.NewApplicationInstallRepository(sharedDB)
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		install, err := installs.FindByID(ctx, installID)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("poll install: %w", err)
		}
		switch install.Status {
		case "ready", "failed", "error":
			return install, nil
		}
		if time.Now().After(deadline) {
			return install, fmt.Errorf("timed out waiting for install %s (last status=%s)", installID, install.Status)
		}
		select {
		case <-parent.Done():
			return install, parent.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
