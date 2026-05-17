package api

import (
	"encoding/json"
	"testing"
)

// Phase-1 feedback loop for the M46 admin-phpMyAdmin SSO bug.
//
// install/phpmyadmin/sso.php:105 rejects the validator response unless
// it has the keys user,password,host,port,db,only_db ALL present, and
// :112 requires db/only_db to be is_string. The admin-all-DBs branch
// of sso_phpmyadmin_validate.go builds an ssoValidateResponse with
// empty DB/OnlyDB; the `,omitempty` json tags then DROP those keys,
// so sso.php fails "unexpected validator payload". Per-user works
// only because it sets db.Name (non-empty) so omitempty keeps them.
//
// This pins the wire contract sso.php depends on. RED with omitempty
// on DB/OnlyDB; GREEN once those keys always serialize.
func TestSSOValidatePayload_AdminCaseSatisfiesSSOPHP(t *testing.T) {
	// The exact shape the admin-sentinel branch emits (empty DB/OnlyDB
	// = "all databases").
	adminResp := ssoValidateResponse{
		User:     "jabali_pma_admin",
		Password: "secret",
		Host:     "localhost",
		Port:     3306,
		Socket:   mariaDBSocketPath,
		// DB, OnlyDB intentionally empty (all-DBs admin handoff).
	}
	b, err := json.Marshal(adminResp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// sso.php:105 — these keys MUST be present (isset).
	for _, k := range []string{"user", "password", "host", "port", "db", "only_db"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("sso.php:105 requires key %q present; admin response is missing it (json=%s)", k, b)
		}
	}
	// sso.php:112 — db / only_db must be JSON strings (empty allowed).
	for _, k := range []string{"db", "only_db"} {
		if _, ok := m[k].(string); !ok {
			t.Fatalf("sso.php:112 requires %q to be a string, got %T", k, m[k])
		}
	}
}
