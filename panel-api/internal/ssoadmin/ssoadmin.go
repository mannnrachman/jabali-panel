// Package ssoadmin owns the single security decision behind the
// admin-all-DBs SSO handoff (ADR-0099): given the engine a consumed
// admin-sentinel token targets, which privileged account and which
// at-rest secret file authorise it.
//
// This was duplicated verbatim in sso_phpmyadmin_validate.go and
// sso_adminer_validate.go (the M46 __M46_ADMIN_ALL__ branch). The
// per-user resolution tails are NOT shared — they genuinely diverge
// by engine/UI (different token repos, decrypt strategies, wire
// shapes), so only this decision is concentrated here. A future
// engine, account rename, or secret-path change is one edit, not a
// two-file lockstep change in security-critical code.
package ssoadmin

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Secret-file locations. Package vars (not consts) only so tests can
// redirect them; production never reassigns. Canonical home — the
// api package's pmaAdminPasswordFile/pgSuperuserPasswordFile copies
// are removed in favour of this.
var (
	pmaAdminPasswordPath = "/etc/jabali-panel/pma-admin.password"
	pgSuperPasswordPath  = "/etc/jabali-panel/postgres.password"
)

var (
	// ErrUnknownEngine: the token's engine is not one we mint
	// admin-scope SSO for.
	ErrUnknownEngine = errors.New("ssoadmin: unknown engine")
	// ErrSecretUnavailable: the privileged secret file is missing or
	// unreadable (agent never provisioned it / perms drift).
	ErrSecretUnavailable = errors.New("ssoadmin: admin secret unreadable")
)

// Credential is the resolved privileged account. Callers format their
// own wire response (phpMyAdmin socket/port vs Adminer driver/server).
type Credential struct {
	Username string
	Password string
}

// AdminCredential resolves the privileged all-DBs account for engine.
// engine is "mariadb" (phpMyAdmin → jabali_pma_admin) or "postgres"
// (Adminer → postgres superuser).
func AdminCredential(engine string) (Credential, error) {
	var username, path string
	switch engine {
	case "mariadb":
		username, path = "jabali_pma_admin", pmaAdminPasswordPath
	case "postgres":
		username, path = "postgres", pgSuperPasswordPath
	default:
		return Credential{}, fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: %v", ErrSecretUnavailable, err)
	}
	return Credential{Username: username, Password: strings.TrimSpace(string(b))}, nil
}
