// `jabali db` cobra subcommands — list / create / delete user
// databases. M41 operator CLI extension. Same DB rows + agent calls
// the REST handler at /api/v1/databases produces; logic mirrored
// here verbatim until the M41 internal/dbops/ refactor lands and
// both call sites converge.
//
// Engine routing (mariadb default, postgres opt-in via
// server_settings.postgres_enabled) preserved. Database name
// validated through the same regex (databaseNameValid). Quota
// check honoured. Username prefix applied for non-admin targets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// Same regex the REST handler enforces. Lower-case start, alnum +
// underscore, max 30 chars (leaves room for the username prefix).
var cliDBNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)

func dbRepoFromDB() repository.DatabaseRepository {
	return repository.NewDatabaseRepository(sharedDB)
}

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "db",
		Aliases: []string{"database"},
		Short:   "Manage user databases (mariadb / postgres)",
	}
	cmd.AddCommand(
		newDBListCmd(),
		newDBCreateCmd(),
		newDBDeleteCmd(),
	)
	return cmd
}

func newDBListCmd() *cobra.Command {
	var userLookup string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List databases (filtered by user, or all)",
		PreRunE: requireDB,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			repo := dbRepoFromDB()
			var rows []models.Database
			if userLookup == "" {
				r, _, err := repo.List(ctx, repository.ListOptions{Offset: 0, Limit: 500})
				if err != nil {
					return err
				}
				rows = r
			} else {
				u, err := resolveUser(ctx, userLookup)
				if err != nil {
					return err
				}
				r, _, err := repo.ListByUserID(ctx, u.ID, repository.ListOptions{Offset: 0, Limit: 500})
				if err != nil {
					return err
				}
				rows = r
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(rows)
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tENGINE\tUSER_ID\tCREATED")
			for _, r := range rows {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					r.ID, r.Name, r.Engine, r.UserID, r.CreatedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&userLookup, "user", "", "Filter by user (email or username)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}

func newDBCreateCmd() *cobra.Command {
	var userLookup, name, engine string
	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a database for a user",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			if userLookup == "" || name == "" {
				return errors.New("--user and --name are required")
			}
			if engine == "" {
				engine = "mariadb"
			}
			if engine != "mariadb" && engine != "postgres" {
				return fmt.Errorf("--engine must be 'mariadb' or 'postgres' (got %q)", engine)
			}
			if !cliDBNameRe.MatchString(name) {
				return fmt.Errorf("invalid database name %q — must match ^[a-z][a-z0-9_]{0,30}$", name)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			user, err := resolveUser(ctx, userLookup)
			if err != nil {
				return err
			}
			if user.Username == nil || *user.Username == "" {
				return fmt.Errorf("user %s has no Linux username — cannot prefix database name", user.ID)
			}

			// Quota check via package's MaxDatabases.
			repo := dbRepoFromDB()
			pkgRepo := repository.NewPackageRepository(sharedDB)
			if user.PackageID != nil && *user.PackageID != "" {
				pkg, err := pkgRepo.FindByID(ctx, *user.PackageID)
				if err == nil && pkg.MaxDatabases > 0 {
					count, err := repo.CountByUserID(ctx, user.ID)
					if err != nil {
						return fmt.Errorf("count databases: %w", err)
					}
					if count >= int64(pkg.MaxDatabases) {
						return fmt.Errorf("quota exceeded: %d/%d databases for user %s",
							count, pkg.MaxDatabases, *user.Username)
					}
				}
			}

			finalName := *user.Username + "_" + name

			exists, err := repo.ExistsByUserAndName(ctx, user.ID, finalName)
			if err != nil {
				return fmt.Errorf("collision check: %w", err)
			}
			if exists {
				return fmt.Errorf("database %q already exists for user %s", finalName, *user.Username)
			}

			// Engine gate — postgres requires server_settings flip.
			if engine == "postgres" {
				ssRepo := repository.NewServerSettingsRepository(sharedDB)
				ss, sErr := ssRepo.Get(ctx)
				if sErr != nil {
					return fmt.Errorf("server_settings: %w", sErr)
				}
				if ss == nil || !ss.PostgresEnabled {
					return errors.New("postgres engine requested but server_settings.postgres_enabled=false; flip it via the admin UI or `UPDATE server_settings SET postgres_enabled=1`")
				}
			}

			// Agent dispatch.
			if engine == "mariadb" {
				_, err = sharedAgent.Call(ctx, "db.create", map[string]any{
					"db_name":   finalName,
					"charset":   "utf8mb4",
					"collation": "utf8mb4_unicode_ci",
				})
			} else {
				_, err = sharedAgent.Call(ctx, "db.postgres.create_db", map[string]any{
					"db_name": finalName,
					"owner":   "postgres",
				})
			}
			if err != nil {
				return fmt.Errorf("agent.%s: %w", engine, err)
			}

			now := time.Now().UTC()
			charset, collation := "utf8mb4", "utf8mb4_unicode_ci"
			if engine == "postgres" {
				charset, collation = "UTF8", ""
			}
			d := &models.Database{
				ID:        ids.NewULID(),
				UserID:    user.ID,
				Name:      finalName,
				Engine:    engine,
				Charset:   charset,
				Collation: collation,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := repo.Create(ctx, d); err != nil {
				return fmt.Errorf("db row insert: %w", err)
			}
			fmt.Fprintf(os.Stdout, "Created database %s (id=%s, engine=%s)\n", finalName, d.ID, engine)
			return nil
		},
	}
	cmd.Flags().StringVar(&userLookup, "user", "", "User (email or username) — required")
	cmd.Flags().StringVar(&name, "name", "", "Database name (without user prefix) — required")
	cmd.Flags().StringVar(&engine, "engine", "mariadb", "Engine: mariadb | postgres")
	return cmd
}

func newDBDeleteCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete a database by ID",
		PreRunE: requireDBAndAgent,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return errors.New("--id is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			repo := dbRepoFromDB()
			d, err := repo.FindByID(ctx, id)
			if err != nil {
				return fmt.Errorf("find: %w", err)
			}

			// Agent drop. Postgres uses a dedicated drop command.
			cmdName := "db.drop"
			if d.Engine == "postgres" {
				cmdName = "db.postgres.drop_db"
			}
			if _, err := sharedAgent.Call(ctx, cmdName, map[string]any{
				"db_name": d.Name,
			}); err != nil {
				return fmt.Errorf("agent.%s: %w", cmdName, err)
			}

			if err := repo.Delete(ctx, d.ID); err != nil {
				return fmt.Errorf("delete row: %w", err)
			}
			fmt.Fprintf(os.Stdout, "Deleted database %s (engine=%s)\n", d.Name, d.Engine)
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Database ID (ULID)")
	return cmd
}

