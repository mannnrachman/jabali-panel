package commands

// M46 Step 5 — database maintenance (ADR-0100). Deliberately NOT
// called "repair": `mysqlcheck --repair` is a no-op on InnoDB (≈all
// jabali DBs), so we run --optimize --analyze and report honestly.
// Runs root-over-socket (MariaDB) / postgres-over-peer (Postgres);
// jabali_panel gains no privilege.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type dbMaintenanceParams struct {
	// Scope is "all" or a single database name.
	Scope string `json:"scope"`
}

type dbMaintenanceResponse struct {
	Summary string `json:"summary"`
}

// dbScopeNameRegex bounds a single-db scope to a safe identifier.
var dbScopeNameRegex = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_$-]{0,63}$`)

func validMaintenanceScope(s string) bool {
	return s == "all" || dbScopeNameRegex.MatchString(s)
}

// dbMaintenanceHandler — MariaDB optimize + analyze.
func dbMaintenanceHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbMaintenanceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if !validMaintenanceScope(p.Scope) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "invalid scope"}
	}
	args := []string{"--optimize", "--analyze"}
	if p.Scope == "all" {
		args = append(args, "--all-databases")
	} else {
		args = append(args, "--databases", p.Scope)
	}
	out, err := exec.CommandContext(ctx, "mariadb-check", args...).CombinedOutput()
	summary := strings.TrimSpace(string(out))
	if err != nil {
		// mysqlcheck exits non-zero on table-level notes too; surface
		// output but mark it errored so the operator can inspect.
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: "mysqlcheck failed: " + dbmTruncate(summary, 800),
		}
	}
	if summary == "" {
		summary = "OK — optimize + analyze completed (InnoDB tables: optimize is a rebuild+analyze; 'repair' is a no-op for InnoDB and was not attempted)."
	}
	return dbMaintenanceResponse{Summary: dbmTruncate(summary, 4000)}, nil
}

// dbPostgresMaintenanceHandler — VACUUM (ANALYZE) + REINDEX. No
// VACUUM FULL by default (ACCESS EXCLUSIVE lock); not exposed here.
func dbPostgresMaintenanceHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p dbMaintenanceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if !validMaintenanceScope(p.Scope) {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "invalid scope"}
	}
	var vac, rei *exec.Cmd
	if p.Scope == "all" {
		vac = exec.CommandContext(ctx, "sudo", "-u", "postgres", "vacuumdb", "--all", "--analyze")
		rei = exec.CommandContext(ctx, "sudo", "-u", "postgres", "reindexdb", "--all")
	} else {
		vac = exec.CommandContext(ctx, "sudo", "-u", "postgres", "vacuumdb", "-d", p.Scope, "--analyze")
		rei = exec.CommandContext(ctx, "sudo", "-u", "postgres", "reindexdb", "-d", p.Scope)
	}
	vOut, vErr := vac.CombinedOutput()
	rOut, rErr := rei.CombinedOutput()
	summary := "vacuumdb:\n" + strings.TrimSpace(string(vOut)) + "\n\nreindexdb:\n" + strings.TrimSpace(string(rOut))
	if vErr != nil || rErr != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: "pg maintenance failed: " + dbmTruncate(summary, 800)}
	}
	if strings.TrimSpace(string(vOut))+strings.TrimSpace(string(rOut)) == "" {
		summary = "OK — VACUUM (ANALYZE) + REINDEX completed."
	}
	return dbMaintenanceResponse{Summary: dbmTruncate(summary, 4000)}, nil
}

func dbmTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}

func init() {
	Default.Register("db.maintenance", dbMaintenanceHandler)
	Default.Register("db.postgres.maintenance", dbPostgresMaintenanceHandler)
}
