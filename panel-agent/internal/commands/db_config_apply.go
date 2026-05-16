package commands

// M46 Step 3 — curated DB config tuner apply (ADR-0098). The reconciler
// renders the desired set from db_tuning_settings and calls these every
// converge tick; the handlers are idempotent (byte-identical desired →
// no reload) so the steady state is cheap and hand-edits are repaired.
//
// MariaDB is the dangerous path: a bad drop-in stops mariadbd and the
// panel loses its own DB. Flow: validate → backup → atomic swap →
// reload/restart → health probe → on failure restore .bak + restart →
// if THAT fails, write an unrecoverable marker (B7) and report it so
// panel-api raises a critical M14 alert.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/dbtuning"
)

type dbConfigApplyParams struct {
	Settings        map[string]string `json:"settings"`
	RestartRequired bool              `json:"restart_required"`
}

type dbConfigApplyResponse struct {
	Changed bool `json:"changed"`
}

const (
	mariaTuningDropIn    = "/etc/mysql/mariadb.conf.d/zz-jabali-tuning.cnf"
	dbConfigBrokenMarker = "/var/lib/jabali-agent/db-config-broken.json"
)

func mysqlPing(ctx context.Context) bool {
	return exec.CommandContext(ctx, "mariadb-admin", "--protocol=socket", "ping").Run() == nil
}

func writeBrokenMarker(engine, detail string) {
	_ = os.MkdirAll(filepath.Dir(dbConfigBrokenMarker), 0o750)
	payload, _ := json.Marshal(map[string]any{
		"engine": engine, "detail": detail, "ts": time.Now().UTC().Format(time.RFC3339),
	})
	_ = os.WriteFile(dbConfigBrokenMarker, payload, 0o640)
}

// dbConfigApplyHandler — MariaDB.
func dbConfigApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbConfigApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("failed to parse params: %v", err)}
	}
	// Defense in depth: re-validate against the allowlist agent-side
	// (panel-api validates too; never trust the caller — ADR-0098).
	if err := dbtuning.ValidateSet("mariadb", p.Settings); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}

	desired := dbtuning.RenderMariaDBDropIn(p.Settings)

	// Drift detection: identical on-disk content → nothing to do, no
	// reload (ADR-0098 "reload only on divergence").
	if cur, err := os.ReadFile(mariaTuningDropIn); err == nil && string(cur) == desired {
		return dbConfigApplyResponse{Changed: false}, nil
	}

	// Best-effort pre-validation (MariaDB 10.6+ --validate-config).
	// Unknown-flag / older server → skip; the probe + rollback below
	// is the real safety net.
	tmp, err := os.CreateTemp(filepath.Dir(mariaTuningDropIn), ".zz-jabali-tuning-*.cnf")
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "create temp: " + err.Error()}
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(desired); err != nil {
		tmp.Close()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "write temp: " + err.Error()}
	}
	tmp.Close()
	_ = os.Chmod(tmpName, 0o644)
	if out, verr := exec.CommandContext(ctx, "mariadbd",
		"--defaults-extra-file="+tmpName, "--validate-config").CombinedOutput(); verr != nil &&
		!strings.Contains(string(out), "unknown option") &&
		!strings.Contains(string(out), "validate-config") {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: "config rejected by mariadbd --validate-config: " + strings.TrimSpace(string(out)),
		}
	}

	// Back up the current drop-in (if any), then atomic-swap.
	bak := mariaTuningDropIn + ".bak"
	hadPrev := false
	if cur, rerr := os.ReadFile(mariaTuningDropIn); rerr == nil {
		hadPrev = true
		_ = os.WriteFile(bak, cur, 0o644)
	}
	if err := os.Rename(tmpName, mariaTuningDropIn); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "swap drop-in: " + err.Error()}
	}

	action := "reload-or-restart"
	if p.RestartRequired {
		action = "restart"
	}
	_ = exec.CommandContext(ctx, "systemctl", action, "mariadb").Run()

	// Health probe with a short grace window.
	ok := false
	for i := 0; i < 10; i++ {
		if mysqlPing(ctx) {
			ok = true
			break
		}
		time.Sleep(time.Second)
	}
	if ok {
		return dbConfigApplyResponse{Changed: true}, nil
	}

	// Rollback to the previous good drop-in (or remove ours if there
	// was none) and restart.
	if hadPrev {
		if cur, rerr := os.ReadFile(bak); rerr == nil {
			_ = os.WriteFile(mariaTuningDropIn, cur, 0o644)
		}
	} else {
		_ = os.Remove(mariaTuningDropIn)
	}
	_ = exec.CommandContext(ctx, "systemctl", "restart", "mariadb").Run()
	for i := 0; i < 10; i++ {
		if mysqlPing(ctx) {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeFailedPrecondition,
				Message: "config rejected: MariaDB failed health check; rolled back to previous config",
			}
		}
		time.Sleep(time.Second)
	}
	// B7: rollback-of-rollback also failed — MariaDB is down. Mark it
	// so panel-api raises a CRITICAL M14 alert instead of the operator
	// learning from a tenant ticket.
	writeBrokenMarker("mariadb", "apply + rollback both failed; MariaDB not answering ping")
	return nil, &agentwire.AgentError{
		Code:    agentwire.CodeInternal,
		Message: "UNRECOVERABLE: MariaDB down after config apply AND rollback; manual intervention required",
	}
}

// dbPostgresConfigApplyHandler — PostgreSQL via ALTER SYSTEM SET +
// pg_reload_conf(). Reversible; restart only for restart-required keys.
func dbPostgresConfigApplyHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbConfigApplyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("failed to parse params: %v", err)}
	}
	if err := dbtuning.ValidateSet("postgres", p.Settings); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
	}
	for _, stmt := range dbtuning.PostgresStatements(p.Settings) {
		if err := pgRunSQL(ctx, stmt); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "ALTER SYSTEM failed: " + err.Error()}
		}
	}
	if p.RestartRequired {
		_ = exec.CommandContext(ctx, "systemctl", "restart", "postgresql").Run()
	} else {
		if err := pgRunSQL(ctx, "SELECT pg_reload_conf()"); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "pg_reload_conf failed: " + err.Error()}
		}
	}
	if exec.CommandContext(ctx, "pg_isready").Run() != nil {
		writeBrokenMarker("postgres", "postgresql not ready after config apply")
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "UNRECOVERABLE: postgresql not ready after config apply"}
	}
	return dbConfigApplyResponse{Changed: true}, nil
}

func init() {
	Default.Register("db.config.apply", dbConfigApplyHandler)
	Default.Register("db.postgres.config.apply", dbPostgresConfigApplyHandler)
}
