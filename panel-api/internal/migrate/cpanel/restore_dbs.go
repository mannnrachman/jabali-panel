package cpanel

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// DBImportResult is returned to the restore-stage caller for
// progress reporting + manifest update.
type DBImportResult struct {
	Created int
	Skipped []string
	// Credentials captures (db_name → DBCredential) for each DB +
	// user pair created during this restore pass. ImportAppConfigs
	// reads it to rewrite WordPress/Drupal/Joomla/Magento config
	// files in the user's homedir with the new (name, user, pass)
	// triple so apps boot against the migrated MariaDB.
	Credentials map[string]DBCredential
}

// DBCredential is one (db_name, db_user, db_pass) row the config-
// rewrite step uses to splice values into wp-config.php and
// friends.
type DBCredential struct {
	DBName   string
	DBUser   string
	Password string // plaintext temp_pwd printed in the manifest line
}

// dbRestoreNameRe mirrors the agent's db.restore validation
// (^[a-zA-Z][a-zA-Z0-9_-]{0,63}$). Kept here so we reject names
// up-front rather than failing at agent dispatch time after a
// db.create succeeded — half-applied state.
var dbRestoreNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// ImportDatabases walks each .sql dump in the parsed tarball,
// derives the destination DB name (jabali-username-prefixed),
// invokes agent db.create + db.restore, and inserts a databases
// row. Idempotency: a name collision (same DB already exists for
// this user) skips the entry rather than failing the whole import
// — resume after a partial failure no-ops on already-imported
// dbs.
//
// agentClient is nullable for unit-test purposes; production
// callers always pass a live client.
func ImportDatabases(
	ctx context.Context,
	dbsRepo repository.DatabaseRepository,
	dbUsersRepo repository.DatabaseUserRepository,
	dbGrantsRepo repository.DatabaseUserGrantRepository,
	agentClient agent.AgentInterface,
	parsed *ParsedTarball,
	targetUserID, targetUsername string,
) (*DBImportResult, error) {
	if dbsRepo == nil {
		return nil, fmt.Errorf("ImportDatabases: dbs repo nil")
	}
	if agentClient == nil {
		return nil, fmt.Errorf("ImportDatabases: agent client nil")
	}
	if parsed == nil {
		return nil, fmt.Errorf("ImportDatabases: parsed nil")
	}
	if targetUserID == "" || targetUsername == "" {
		return nil, fmt.Errorf("ImportDatabases: targetUserID/targetUsername empty")
	}

	res := &DBImportResult{}
	for _, dumpPath := range parsed.MySQLDumps {
		base := strings.TrimSuffix(filepath.Base(dumpPath), ".sql")
		// Strip the cPanel-side username prefix if present
		// (cpuser_blogdb → blogdb). Falls back to the full base
		// if no underscore — the operator's source DB name is
		// what we keep.
		logical := base
		if idx := strings.Index(base, "_"); idx > 0 && idx < len(base)-1 {
			logical = base[idx+1:]
		}
		// Apply jabali prefix to land in the destination
		// namespace. Same shape `databases` REST handler produces.
		finalName := targetUsername + "_" + logical
		if !dbRestoreNameRe.MatchString(finalName) {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: derived name %q rejects validator", dumpPath, finalName))
			continue
		}

		// Idempotent collision check — resume after partial
		// failure no-ops on an already-imported DB.
		exists, err := dbsRepo.ExistsByUserAndName(ctx, targetUserID, finalName)
		if err != nil {
			return res, fmt.Errorf("collision check %s: %w", finalName, err)
		}
		if exists {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %q already imported", dumpPath, finalName))
			continue
		}

		// agent.db.create → materialise empty schema. Tight
		// timeout — CREATE DATABASE is single-statement.
		createCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = agentClient.Call(createCtx, "db.create", map[string]any{
			"db_name":   finalName,
			"charset":   "utf8mb4",
			"collation": "utf8mb4_unicode_ci",
		})
		cancel()
		if err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: db.create failed: %v", finalName, err))
			continue
		}

		// agent.db.restore → mariadb < dump.sql. Generous
		// timeout — multi-GB dumps take real time. 30 minutes
		// per DB is generous; stuck-process kill is a separate
		// concern handled by the transient unit's own timeout.
		restoreCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		// reset_before_restore=true: ADR-0095 amendment 2026-05-12
		// stage idempotency. Migration restores partway, fails, then
		// resume-retry re-streams the dump. CREATE TABLE inside the
		// dump conflicts unless we DROP+CREATE the DB first. Migration
		// targets are freshly-provisioned by jabali — destroying the
		// DB is safe (M35 spec restores INTO new accounts, never over
		// operator data).
		_, err = agentClient.Call(restoreCtx, "db.restore", map[string]any{
			"db_name":              finalName,
			"path":                 dumpPath,
			"reset_before_restore": true,
		})
		cancel()
		if err != nil {
			// Best-effort cleanup: drop the empty DB so resume
			// doesn't trip the collision check + skip on retry.
			dropCtx, dcancel := context.WithTimeout(ctx, 10*time.Second)
			_, _ = agentClient.Call(dropCtx, "db.drop", map[string]any{"db_name": finalName})
			dcancel()
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: db.restore failed: %v", finalName, err))
			continue
		}

		row := &models.Database{
			ID:        ids.NewULID(),
			UserID:    targetUserID,
			Name:      finalName,
			Engine:    "mariadb",
			Charset:   "utf8mb4",
			Collation: "utf8mb4_unicode_ci",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := dbsRepo.Create(ctx, row); err != nil {
			// Schema is materialised, dump is loaded, but the
			// panel's row failed to land. Operator can SQL the
			// row in by hand from logs, or re-run import (the
			// idempotent check above will skip the existing
			// DB, but databases row insert here is what fails).
			// Surface the error rather than silent — restore
			// stage caller decides whether to retry.
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: databases row insert failed: %v", finalName, err))
			continue
		}
		res.Created++

		// Best-effort: create a MariaDB user with the same name as
		// the DB and GRANT ALL. Failure records a warning but never
		// rolls back the already-restored DB.
		if dbUsersRepo != nil && dbGrantsRepo != nil {
			plainPwd := ids.NewULID()
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(plainPwd), bcrypt.DefaultCost)
			if hashErr != nil {
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s: bcrypt db user: %v", finalName, hashErr))
			} else {
				userCtx, userCancel := context.WithTimeout(ctx, 30*time.Second)
				_, userErr := agentClient.Call(userCtx, "db_user.create", map[string]any{
					"db_user_name": finalName,
					"password":     plainPwd,
				})
				userCancel()
				if userErr != nil {
					res.Skipped = append(res.Skipped, fmt.Sprintf("%s: db_user.create: %v", finalName, userErr))
				} else {
					duRow := &models.DatabaseUser{
						ID:           ids.NewULID(),
						UserID:       targetUserID,
						Username:     finalName,
						Engine:       "mariadb",
						PasswordHash: string(hash),
						CreatedAt:    time.Now().UTC(),
						UpdatedAt:    time.Now().UTC(),
					}
					if duErr := dbUsersRepo.Create(ctx, duRow); duErr != nil {
						res.Skipped = append(res.Skipped, fmt.Sprintf("%s: database_users row: %v", finalName, duErr))
					} else {
						grantCtx, grantCancel := context.WithTimeout(ctx, 30*time.Second)
						_, grantErr := agentClient.Call(grantCtx, "db_user.grant", map[string]any{
							"db_name":      finalName,
							"db_user_name": finalName,
							"privileges":   []string{"ALL"},
						})
						grantCancel()
						if grantErr != nil {
							res.Skipped = append(res.Skipped, fmt.Sprintf("%s: db_user.grant: %v", finalName, grantErr))
						} else {
							gRow := &models.DatabaseUserGrant{
								ID:             ids.NewULID(),
								DatabaseID:     row.ID,
								DatabaseUserID: duRow.ID,
								GrantLevel:     "rw",
								Privileges:     "ALL",
								CreatedAt:      time.Now().UTC(),
								UpdatedAt:      time.Now().UTC(),
							}
							if gErr := dbGrantsRepo.Create(ctx, gRow); gErr != nil {
								res.Skipped = append(res.Skipped, fmt.Sprintf("%s: database_user_grants row: %v", finalName, gErr))
							} else {
								res.Skipped = append(res.Skipped, fmt.Sprintf(
									"%s: db_user created (temp_pwd=%s) — change via panel",
									finalName, plainPwd))
								// Stash (name, user, plaintext-pwd) so the
								// config-rewrite step can splice values
								// into wp-config.php / configuration.php /
								// settings.php / app/etc/env.php files.
								if res.Credentials == nil {
									res.Credentials = map[string]DBCredential{}
								}
								res.Credentials[finalName] = DBCredential{
									DBName:   finalName,
									DBUser:   finalName,
									Password: plainPwd,
								}
							}
						}
					}
				}
			}
		}
	}
	return res, nil
}
