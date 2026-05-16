package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// Validation-only paths (no mysql/psql in CI). assertAgentCode lives in
// db_root_password_test.go (same package).

func TestDBConfigApply_Validation(t *testing.T) {
	// Bad JSON.
	_, err := dbConfigApplyHandler(context.Background(), json.RawMessage(`nope`))
	assertAgentCode(t, err, agentwire.CodeInvalidArgument)
	// Unknown / out-of-range key rejected agent-side (defense in depth).
	_, err = dbConfigApplyHandler(context.Background(),
		json.RawMessage(`{"settings":{"evil":"1"}}`))
	assertAgentCode(t, err, agentwire.CodeInvalidArgument)
	_, err = dbPostgresConfigApplyHandler(context.Background(),
		json.RawMessage(`{"settings":{"max_connections":"999999999"}}`))
	assertAgentCode(t, err, agentwire.CodeInvalidArgument)
}

func TestDBMaintenance_ScopeValidation(t *testing.T) {
	for _, bad := range []string{`{"scope":"a; DROP"}`, `{"scope":"../etc"}`, `bad`} {
		_, err := dbMaintenanceHandler(context.Background(), json.RawMessage(bad))
		assertAgentCode(t, err, agentwire.CodeInvalidArgument)
		_, err = dbPostgresMaintenanceHandler(context.Background(), json.RawMessage(bad))
		assertAgentCode(t, err, agentwire.CodeInvalidArgument)
	}
}

func TestDBKill_IDValidation(t *testing.T) {
	for _, bad := range []string{`{"id":"abc"}`, `{"id":"1; KILL 2"}`, `{"id":""}`, `x`} {
		_, err := dbKillHandler(context.Background(), json.RawMessage(bad))
		assertAgentCode(t, err, agentwire.CodeInvalidArgument)
		_, err = dbPostgresTerminateHandler(context.Background(), json.RawMessage(bad))
		assertAgentCode(t, err, agentwire.CodeInvalidArgument)
	}
}

func TestValidMaintenanceScope(t *testing.T) {
	ok := []string{"all", "wp_db", "alice_main", "DB-1"}
	bad := []string{"", "a b", "x;y", "../e", "a'b", "a`b"}
	for _, s := range ok {
		if !validMaintenanceScope(s) {
			t.Fatalf("scope %q should be valid", s)
		}
	}
	for _, s := range bad {
		if validMaintenanceScope(s) {
			t.Fatalf("scope %q should be REJECTED", s)
		}
	}
}
