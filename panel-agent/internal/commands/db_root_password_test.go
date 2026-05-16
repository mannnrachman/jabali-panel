package commands

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// Validation paths only — the success path execs `mysql`/`psql` which
// aren't present in CI. The MariaDB socket-preservation guard is
// covered by the panel-api handler test + the runbook smoke.
func TestDBRootSetPasswordHandler_Validation(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		code string
	}{
		{"bad json", `not-json`, agentwire.CodeInvalidArgument},
		{"empty password", `{"new_password":""}`, agentwire.CodeInvalidArgument},
		{"missing password", `{}`, agentwire.CodeInvalidArgument},
	} {
		t.Run("mariadb/"+tc.name, func(t *testing.T) {
			_, err := dbRootSetPasswordHandler(context.Background(), json.RawMessage(tc.raw))
			assertAgentCode(t, err, tc.code)
		})
		t.Run("postgres/"+tc.name, func(t *testing.T) {
			_, err := dbPostgresSuperuserSetPasswordHandler(context.Background(), json.RawMessage(tc.raw))
			assertAgentCode(t, err, tc.code)
		})
	}
}

// The ADR-0097 invariant in code form: the MariaDB ALTER statement must
// list unix_socket FIRST and keep mysql_native_password as the OR
// backup. A regression here silently deletes socket auth.
func TestDBRoot_MariaDBStatementPreservesSocketAuth(t *testing.T) {
	const want = "ALTER USER 'root'@'localhost' IDENTIFIED VIA unix_socket OR mysql_native_password USING PASSWORD("
	// The literal lives in the handler source; assert the exact,
	// order-sensitive prefix is what we build (guards the grammar
	// without needing a live mysqld).
	got := mariadbRootAlterPrefix
	if !strings.HasPrefix(got, want) {
		t.Fatalf("MariaDB root ALTER must start with %q (socket-first, ADR-0097); got %q", want, got)
	}
}

func assertAgentCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	ae, ok := err.(*agentwire.AgentError)
	if !ok {
		t.Fatalf("expected *agentwire.AgentError, got %T (%v)", err, err)
	}
	if ae.Code != code {
		t.Fatalf("expected code %s, got %s (%s)", code, ae.Code, ae.Message)
	}
}
