package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/apps"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Direct-DB helpers for `jabali app *`. Mirrors the pattern in cli_ops.go
// (user/domain) — straight to the DB so the CLI keeps working under
// a CLI environment where the Kratos session cookie isn't available.
//
// Scope is intentionally narrow: registry + list + get + delete. The
// HTTP create handler (api.applications.go) carries 16 per-app kicker
// goroutines and the DB-chain provisioner — all package-private. Adding
// `app create` direct-DB requires extracting that pipeline into a
// shared service package; tracked as a separate ticket.

// listAppRegistry returns every app descriptor the panel knows about.
// Build the registry in-process so the CLI doesn't have to hit a running
// jabali-panel (matches list/delete). Mutates no state.
func listAppRegistry() ([]apps.App, error) {
	reg := apps.New()
	if err := apps.RegisterDefaults(reg); err != nil {
		return nil, fmt.Errorf("register app defaults: %w", err)
	}
	return reg.List(), nil
}

// listAppsDirect returns every install ordered by created_at ASC. Page
// size 1000 matches listUsersDirect/listDomainsDirect — enough for any
// single-operator install.
func listAppsDirect(ctx context.Context) ([]models.ApplicationInstall, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	installs, _, err := repository.NewApplicationInstallRepository(sharedDB).
		List(ctx, repository.ListOptions{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	return installs, nil
}

// getAppDirect fetches one install by ID. Returns a typed not-found so
// the CLI can render a clean message instead of a wrapped GORM error.
func getAppDirect(ctx context.Context, installID string) (*models.ApplicationInstall, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	install, err := repository.NewApplicationInstallRepository(sharedDB).FindByID(ctx, installID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("application %q not found", installID)
		}
		return nil, fmt.Errorf("lookup application: %w", err)
	}
	return install, nil
}

// deleteAppDirect mirrors the side effects of api.createDeleteAndKickAgent
// (panel-api/internal/api/wordpress.go). Order matters because the
// fk_wpinstalls_db FK is RESTRICT — see the comments in the HTTP path
// for the rationale; do not reorder without re-reading them.
//
// Steps:
//  1. Resolve domain + os_user + db user/grants for the agent payload
//  2. Mark status=deleting so concurrent dashboards stop trying to read it
//  3. Fire agent app.delete (files + nginx placeholder restore)
//  4. Drop grants → mariadb user → install row → mariadb database
//
// Errors during the agent calls are NOT fatal — the panel rows are still
// dropped so the operator can re-run after fixing the host-side issue.
// Each agent failure logs at warn level; the final error returned is
// only set when a critical step (status update, install delete) fails.
func deleteAppDirect(ctx context.Context, installID string) (*models.ApplicationInstall, error) {
	if err := initConfig(); err != nil {
		return nil, err
	}
	if err := initDB(); err != nil {
		return nil, err
	}
	if err := initAgent(); err != nil {
		return nil, err
	}

	installs := repository.NewApplicationInstallRepository(sharedDB)
	domains := repository.NewDomainRepository(sharedDB)
	users := userRepo()
	dbs := repository.NewDatabaseRepository(sharedDB)
	dbUsers := repository.NewDatabaseUserRepository(sharedDB)
	dbGrants := repository.NewDatabaseUserGrantRepository(sharedDB)

	install, err := installs.FindByID(ctx, installID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("application %q not found", installID)
		}
		return nil, fmt.Errorf("lookup application: %w", err)
	}

	domain, err := domains.FindByID(ctx, install.DomainID)
	if err != nil {
		return nil, fmt.Errorf("lookup domain: %w", err)
	}

	owner, err := users.FindByID(ctx, install.UserID)
	if err != nil {
		return nil, fmt.Errorf("lookup owner: %w", err)
	}
	if owner.Username == nil || *owner.Username == "" {
		return nil, fmt.Errorf("owner %q has no linux username (orphaned install?)", install.UserID)
	}
	osUser := *owner.Username

	// Pre-resolve the DB user before we mark the install deleting so a
	// FK lookup failure aborts before we mutate anything.
	var dbUserID, dbUserUsername string
	if install.DBIDOr() != "" {
		grants, gErr := dbGrants.ListByDatabaseID(ctx, install.DBIDOr())
		if gErr == nil && len(grants) > 0 {
			dbUserID = grants[0].DatabaseUserID
			if dbu, duErr := dbUsers.FindByID(ctx, dbUserID); duErr == nil && dbu != nil {
				dbUserUsername = dbu.Username
			}
		}
	}

	if err := installs.UpdateStatus(ctx, installID, "deleting", nil, nil); err != nil {
		return nil, fmt.Errorf("mark deleting: %w", err)
	}

	appType := install.AppType
	if appType == "" {
		// Pre-M19 rows had no AppType; the column default backfilled to
		// "wordpress" but treat empty defensively to avoid dispatching
		// app.delete with an empty discriminator (which the agent would
		// 400 on).
		appType = "wordpress"
	}

	agentCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if _, agentErr := sharedAgent.Call(agentCtx, "app.delete", map[string]any{
		"app_type":     appType,
		"install_id":   installID,
		"os_user":      osUser,
		"docroot":      domain.DocRoot,
		"subdirectory": install.Subdirectory,
		"domain":       domain.Name,
	}); agentErr != nil {
		// Match the HTTP path: stamp last_error onto the row and
		// surface to the operator. We still drop the panel rows below
		// so a fresh install of the same slot doesn't hit a 409.
		errMsg := truncString(fmt.Sprintf("agent delete failed: %v", agentErr), 1024)
		if uErr := installs.UpdateStatus(ctx, installID, "failed", &errMsg, nil); uErr != nil {
			slog.Warn("cli app delete: status update failed", "err", uErr)
		}
		slog.Warn("cli app delete: agent app.delete failed — continuing with DB cleanup",
			"install_id", installID, "err", agentErr)
	}

	// DB-side cleanup. Order is grants → mariadb user → install row →
	// mariadb database. Reordering breaks fk_wpinstalls_db RESTRICT.
	if dbUserID != "" {
		if userGrants, gErr := dbGrants.ListByDatabaseUserID(ctx, dbUserID); gErr == nil {
			for _, g := range userGrants {
				if dErr := dbGrants.Delete(ctx, g.ID); dErr != nil {
					slog.Warn("cli app delete: drop grant failed", "grant_id", g.ID, "err", dErr)
				}
			}
		}
		if dbUserUsername != "" {
			if _, agentErr := sharedAgent.Call(agentCtx, "db_user.drop", map[string]any{"db_user_name": dbUserUsername}); agentErr != nil {
				slog.Warn("cli app delete: db_user.drop failed", "db_user", dbUserUsername, "err", agentErr)
			}
		}
		if dErr := dbUsers.Delete(ctx, dbUserID); dErr != nil {
			slog.Warn("cli app delete: db_user row delete failed", "db_user_id", dbUserID, "err", dErr)
		}
	}

	if dErr := installs.Delete(ctx, installID); dErr != nil {
		return install, fmt.Errorf("delete install row: %w", dErr)
	}

	if install.DBIDOr() != "" {
		if db, dbErr := dbs.FindByID(ctx, install.DBIDOr()); dbErr == nil && db != nil {
			if _, agentErr := sharedAgent.Call(agentCtx, "db.drop", map[string]any{"db_name": db.Name}); agentErr != nil {
				slog.Warn("cli app delete: db.drop failed", "db_name", db.Name, "err", agentErr)
			}
		}
		if dErr := dbs.Delete(ctx, install.DBIDOr()); dErr != nil {
			slog.Warn("cli app delete: database row delete failed", "db_id", install.DBIDOr(), "err", dErr)
		}
	}

	return install, nil
}

// truncString clips s to max bytes (no UTF-8 awareness — last_error is
// stored as VARCHAR(1024) and the column truncates anyway; this just
// avoids sending an oversized payload to MariaDB).
func truncString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
