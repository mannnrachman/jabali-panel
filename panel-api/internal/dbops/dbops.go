// Package dbops is the shared create/delete/list logic for user
// databases (M41 ADR target 0083).
//
// Both the REST handler at panel-api/internal/api/databases.go and
// the `jabali db` cobra subcommand at panel-api/cmd/server/db_cmd.go
// call into this package so validation, agent dispatch, prefix
// handling, and quota enforcement live in one place. Either caller
// is a thin wrapper that builds CreateInput / DeleteInput, calls
// the function, and renders the result in its own format (JSON
// envelope vs text/JSON for CLI).
//
// Error model: typed Err* sentinels so HTTP can map to status codes
// (errors.Is(err, ErrNameInvalid) → 400) and CLI can exit with
// distinct codes. Wrap with %w to preserve the chain.
package dbops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// AgentCaller is the slice of agent functionality dbops needs.
// Mirrors the interface used by eventsources so tests can supply
// a one-method fake without pulling in the full agent.Client.
type AgentCaller interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// Deps wires the four collaborator repos + the agent. Every field
// is required; zero values cause Create/Delete to return ErrDeps.
type Deps struct {
	Users          repository.UserRepository
	Packages       repository.PackageRepository
	Databases      repository.DatabaseRepository
	ServerSettings repository.ServerSettingsRepository
	Agent          AgentCaller
}

// CreateInput is the shared input shape. Both callers (REST + CLI)
// build this from their own argument parsing.
//
// AsAdmin = true skips the username-prefix step (admin can create
// arbitrary DB names) and still enforces the engine + name regex
// gates. AsAdmin = false applies `<username>_` prefix to the raw
// name, matching the REST handler's pre-M41 behaviour exactly.
type CreateInput struct {
	UserID  string
	RawName string // user-supplied; gets prefixed when !AsAdmin
	Engine  string // "" → "mariadb", "mariadb", "postgres"
	AsAdmin bool
}

// DeleteInput identifies a database to drop. ID is the panel-side
// ULID — caller looks up by name → ID first if needed.
type DeleteInput struct {
	ID string
}

// nameRe is the per-spec database name regex shared by both callers.
// Lowercase start, alnum + underscore, max 30 chars (leaves room
// for the username prefix at insert time).
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

// Sentinel errors. Callers use errors.Is to map to HTTP / exit code.
var (
	ErrDeps           = errors.New("dbops: dependencies not wired")
	ErrUserNotFound   = errors.New("dbops: user not found")
	ErrUserNoUsername = errors.New("dbops: user has no Linux username")
	ErrNameInvalid    = errors.New("dbops: invalid database name")
	ErrEngineInvalid  = errors.New("dbops: invalid engine")
	ErrPostgresOff    = errors.New("dbops: postgres engine disabled in server_settings")
	ErrQuotaExceeded  = errors.New("dbops: package database quota exceeded")
	ErrNameTaken      = errors.New("dbops: database name already exists for user")
	ErrAgentFailed    = errors.New("dbops: agent dispatch failed")
	ErrInternal       = errors.New("dbops: internal error")
	ErrNotFound       = errors.New("dbops: database not found")
)

// Create materialises a database and inserts the panel-side row.
// Returns the persisted *models.Database on success.
//
// Pipeline (matches the pre-M41 REST handler exactly so the
// behavioural change set is "REST + CLI now share code", not
// "REST + CLI behave subtly differently"):
//   1. validate input + load user
//   2. compute final prefixed name
//   3. quota check via package.MaxDatabases
//   4. collision check on (user_id, finalName)
//   5. engine gate (postgres requires server_settings)
//   6. agent.db.create / db.postgres.create_db
//   7. insert databases row
func Create(ctx context.Context, d Deps, in CreateInput) (*models.Database, error) {
	if d.Users == nil || d.Packages == nil || d.Databases == nil || d.Agent == nil {
		return nil, ErrDeps
	}
	if in.UserID == "" || in.RawName == "" {
		return nil, fmt.Errorf("%w: user_id and name required", ErrNameInvalid)
	}
	if !nameRe.MatchString(in.RawName) {
		return nil, fmt.Errorf("%w: name must match ^[a-z][a-z0-9_]{0,30}$", ErrNameInvalid)
	}
	engine := in.Engine
	if engine == "" {
		engine = "mariadb"
	}
	if engine != "mariadb" && engine != "postgres" {
		return nil, fmt.Errorf("%w: must be 'mariadb' or 'postgres'", ErrEngineInvalid)
	}

	user, err := d.Users.FindByID(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("%w: load user: %v", ErrInternal, err)
	}
	if !in.AsAdmin && (user.Username == nil || *user.Username == "") {
		return nil, ErrUserNoUsername
	}

	// Quota check.
	if user.PackageID != nil && *user.PackageID != "" {
		pkg, err := d.Packages.FindByID(ctx, *user.PackageID)
		if err == nil && pkg.MaxDatabases > 0 {
			count, err := d.Databases.CountByUserID(ctx, user.ID)
			if err != nil {
				return nil, fmt.Errorf("%w: count databases: %v", ErrInternal, err)
			}
			if count >= int64(pkg.MaxDatabases) {
				return nil, fmt.Errorf("%w: %d/%d databases", ErrQuotaExceeded, count, pkg.MaxDatabases)
			}
		}
	}

	finalName := in.RawName
	if !in.AsAdmin {
		finalName = *user.Username + "_" + in.RawName
	}

	exists, err := d.Databases.ExistsByUserAndName(ctx, user.ID, finalName)
	if err != nil {
		return nil, fmt.Errorf("%w: collision check: %v", ErrInternal, err)
	}
	if exists {
		return nil, fmt.Errorf("%w: %q", ErrNameTaken, finalName)
	}

	// Postgres gate — server_settings flip required.
	if engine == "postgres" {
		if d.ServerSettings == nil {
			return nil, fmt.Errorf("%w: server_settings unwired", ErrInternal)
		}
		ss, sErr := d.ServerSettings.Get(ctx)
		if sErr != nil {
			return nil, fmt.Errorf("%w: server_settings: %v", ErrInternal, sErr)
		}
		if ss == nil || !ss.PostgresEnabled {
			return nil, ErrPostgresOff
		}
	}

	// Agent dispatch.
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if engine == "mariadb" {
		_, err = d.Agent.Call(agentCtx, "db.create", map[string]any{
			"db_name":   finalName,
			"charset":   "utf8mb4",
			"collation": "utf8mb4_unicode_ci",
		})
	} else {
		_, err = d.Agent.Call(agentCtx, "db.postgres.create_db", map[string]any{
			"db_name": finalName,
			"owner":   "postgres",
		})
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentFailed, err)
	}

	now := time.Now().UTC()
	charset, collation := "utf8mb4", "utf8mb4_unicode_ci"
	if engine == "postgres" {
		charset, collation = "UTF8", ""
	}
	row := &models.Database{
		ID:        ids.NewULID(),
		UserID:    user.ID,
		Name:      finalName,
		Engine:    engine,
		Charset:   charset,
		Collation: collation,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := d.Databases.Create(ctx, row); err != nil {
		return nil, fmt.Errorf("%w: row insert: %v", ErrInternal, err)
	}
	return row, nil
}

// Delete drops the agent-side schema then the panel-side row.
// Errors before agent dispatch return ErrNotFound / ErrDeps;
// agent failures wrap ErrAgentFailed; row delete failure wraps
// ErrInternal. Caller surfaces appropriately.
func Delete(ctx context.Context, d Deps, in DeleteInput) error {
	if d.Databases == nil || d.Agent == nil {
		return ErrDeps
	}
	if in.ID == "" {
		return fmt.Errorf("%w: id required", ErrNotFound)
	}
	row, err := d.Databases.FindByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: find: %v", ErrInternal, err)
	}
	cmdName := "db.drop"
	if row.Engine == "postgres" {
		cmdName = "db.postgres.drop_db"
	}
	agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := d.Agent.Call(agentCtx, cmdName, map[string]any{
		"db_name": row.Name,
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrAgentFailed, err)
	}
	if err := d.Databases.Delete(ctx, row.ID); err != nil {
		return fmt.Errorf("%w: delete row: %v", ErrInternal, err)
	}
	return nil
}
