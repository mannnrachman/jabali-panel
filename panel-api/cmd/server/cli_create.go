package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/kratosclient"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

// cliUserInput covers the one-shot fields `jabali user create` accepts. It
// intentionally mirrors the HTTP `createUserRequest` shape in
// internal/api/users.go — if a flag exists here, the API has it too. No
// --skip-provision yet because the HTTP handler's skip-provision path is
// admin-test-only and CLI operators always want OS provisioning.
type cliUserInput struct {
	Email     string
	Password  string
	NameFirst string
	NameLast  string
	IsAdmin   bool
}

// createUserDirect creates a panel user row + (optionally) a Kratos identity
// + (optionally) an OS user via the agent, using the same compensating
// transaction as internal/api/users.go but without the HTTP round-trip.
//
// This exists because M20 switched the API middleware to Kratos cookies, so
// the CLI's legacy-JWT mintCLIToken path can no longer reach /api/v1/users.
// Operators who want to drive user creation from a root shell (install
// scripts, recovery tooling) need a path that doesn't require a browser
// session. Direct-DB + shared helpers keeps the invariants the HTTP handler
// already enforces (ADR-0003: one write path — this path and the HTTP path
// both land in the same Kratos/agent call sequence).
//
// Returns the created user + a non-fatal provisioning warning if the OS
// user.create agent call failed (panel row + Kratos identity are kept —
// operator can retry provisioning).
func createUserDirect(ctx context.Context, in cliUserInput) (*models.User, string, error) {
	if err := initConfig(); err != nil {
		return nil, "", err
	}
	if err := initDB(); err != nil {
		return nil, "", err
	}
	if err := initAgent(); err != nil {
		return nil, "", err
	}

	if in.Email == "" || in.Password == "" {
		return nil, "", fmt.Errorf("--email and --password are required")
	}
	if len(in.Password) < 10 {
		return nil, "", fmt.Errorf("password must be at least 10 characters")
	}

	users := userRepo()

	// Username derivation for regular users. Admins have nil username — they
	// don't own /home/<user>, so no domain hosting.
	var effectiveUsername *string
	if !in.IsAdmin {
		derived := cliLinuxUserFromEmail(in.Email)
		if derived == "" || !cliValidUsername(derived) {
			return nil, "", fmt.Errorf("could not derive a valid POSIX username from email %q — run the HTTP API with an explicit --username instead", in.Email)
		}
		effectiveUsername = &derived
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash password: %w", err)
	}

	u := &models.User{
		ID:           ids.NewULID(),
		Email:        in.Email,
		Username:     effectiveUsername,
		NameFirst:    in.NameFirst,
		NameLast:     in.NameLast,
		PasswordHash: string(hash),
		IsAdmin:      in.IsAdmin,
	}
	if err := users.Create(ctx, u); err != nil {
		return nil, "", fmt.Errorf("create user row: %w", err)
	}

	// M20: atomic Kratos identity. Compensating delete on failure so retries
	// don't hit a unique-email conflict from a ghost panel row.
	if sharedCfg.Auth.Kratos.PublicURL != "" {
		k := kratosclient.NewClient(sharedCfg.Auth.Kratos.PublicURL, sharedCfg.Auth.Kratos.AdminURL)
		traits := kratosclient.AdminTraits{Email: u.Email, IsAdmin: u.IsAdmin}
		if u.Username != nil {
			traits.Username = *u.Username
		}
		identityID, err := k.CreateIdentityWithPassword(ctx, traits, u.PasswordHash)
		if err != nil {
			if delErr := users.Delete(ctx, u.ID); delErr != nil {
				slog.Error("cli create: kratos failed AND panel rollback failed — orphan row",
					"user_id", u.ID, "email", u.Email, "kratos_err", err, "rollback_err", delErr)
			}
			return nil, "", fmt.Errorf("create kratos identity: %w", err)
		}
		u.KratosIdentityID = &identityID
		if err := users.LinkKratosIdentity(ctx, u.ID, identityID); err != nil {
			if delErr := k.DeleteIdentity(ctx, identityID); delErr != nil {
				slog.Error("cli create: panel link failed AND kratos rollback failed — orphan identity",
					"identity_id", identityID, "link_err", err, "rollback_err", delErr)
			}
			if delErr := users.Delete(ctx, u.ID); delErr != nil {
				slog.Error("cli create: panel link failed AND panel rollback failed",
					"user_id", u.ID, "rollback_err", delErr)
			}
			return nil, "", fmt.Errorf("link kratos identity: %w", err)
		}
	}

	// Best-effort OS user provisioning. Same semantics as the HTTP handler:
	// failure is non-fatal — panel + Kratos rows stay, caller sees a warning.
	var warning string
	if sharedAgent != nil && !in.IsAdmin && effectiveUsername != nil {
		agentCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := sharedAgent.Call(agentCtx, "user.create", map[string]any{
			"username": *effectiveUsername,
			"home_dir": "/home/" + *effectiveUsername,
			"shell":    "/bin/bash",
			"password": in.Password,
		}); err != nil {
			warning = "user saved but OS provisioning failed: " + err.Error()
		}
	}

	return u, warning, nil
}

// cliDomainInput — same scoping logic as cliUserInput. Admins can't own
// domains (same 400 as the HTTP handler).
type cliDomainInput struct {
	Name    string
	UserID  string // ULID of the owning (non-admin) user
	DocRoot string // optional — defaults to /home/<user>/domains/<name>/public_html
}

// createDomainDirect replicates the non-auth side of internal/api/domains.go
// create handler: owner must exist + be non-admin + have a username + pass
// package quota. On success the caller should trigger a reconcile tick so
// the nginx vhost materialises.
//
// Second return is a slice of soft warnings (DNS autoconfig conflicts, the
// agent being unavailable so email couldn't auto-enable, etc.) that the CLI
// front-end prints to stderr. The domain itself is created regardless; only
// hard errors (row insert conflict, bad input) return err != nil.
func createDomainDirect(ctx context.Context, in cliDomainInput) (*models.Domain, []string, error) {
	if err := initConfig(); err != nil {
		return nil, nil, err
	}
	if err := initDB(); err != nil {
		return nil, nil, err
	}

	if in.Name == "" || in.UserID == "" {
		return nil, nil, fmt.Errorf("--name and --user are required")
	}

	domains := domainRepoFromDB()
	packages := packageRepoFromDB()

	// Accept email / username / ULID — same resolver as the other user-
	// facing CLIs so operators don't have to copy-paste ULIDs.
	owner, err := resolveUser(ctx, in.UserID)
	if err != nil {
		return nil, nil, err
	}
	if owner.IsAdmin {
		return nil, nil, fmt.Errorf("admin users cannot host domains — create a regular user")
	}
	if owner.Username == nil || *owner.Username == "" {
		return nil, nil, fmt.Errorf("user %q has no username — inconsistent state", owner.ID)
	}

	// All subsequent DB ops use the resolved ULID, not the free-form
	// spec the operator passed — email / username lookups land here via
	// resolveUser and d.UserID must always be the real ID.
	ownerID := owner.ID

	// Package-quota check — matches the HTTP handler (409 domain_quota_exceeded).
	if owner.PackageID != nil && *owner.PackageID != "" {
		count, err := domains.CountByUserID(ctx, ownerID)
		if err != nil {
			return nil, nil, fmt.Errorf("count existing domains: %w", err)
		}
		pkg, err := packages.FindByID(ctx, *owner.PackageID)
		if err == nil && pkg.MaxDomains > 0 && count >= int64(pkg.MaxDomains) {
			return nil, nil, fmt.Errorf("package quota exceeded: %d/%d domains", count, pkg.MaxDomains)
		}
	}

	docRoot := in.DocRoot
	if docRoot == "" {
		docRoot = "/home/" + *owner.Username + "/domains/" + in.Name + "/public_html"
	}

	now := time.Now().UTC()
	d := &models.Domain{
		ID:         ids.NewULID(),
		UserID:     ownerID,
		Name:       in.Name,
		DocRoot:    docRoot,
		IsEnabled:  true,
		SSLEnabled: true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := domains.Create(ctx, d); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, nil, fmt.Errorf("domain %q already exists", in.Name)
		}
		return nil, nil, fmt.Errorf("create domain row: %w", err)
	}

	// Auto-enable email. Best-effort — if the agent's down or Stalwart
	// refuses the domain name, we record the reason as a soft warning
	// and the operator can retry via `jabali domain email-enable <name>`
	// or the Email tab in the UI.
	var warnings []string
	if err := initAgent(); err != nil {
		warnings = append(warnings, fmt.Sprintf("email auto-enable skipped: agent unavailable (%v)", err))
	} else {
		deps := newDomainEmailDepsFromGlobals()
		_, dnsWarnings, err := enableDomainEmailDirect(ctx, deps, d)
		if err != nil {
			warnings = append(warnings,
				fmt.Sprintf("email auto-enable failed (can retry with `jabali domain email-enable %s`): %v", d.Name, err))
		} else {
			warnings = append(warnings, dnsWarnings...)
		}
	}

	// The reconciler picks up the new row within 60s (default interval). No
	// inline nginx call — matches the HTTP handler, keeps ADR-0013's inline
	// best-effort pattern confined to users.
	return d, warnings, nil
}

// ---------- local username helpers (duplicated from internal/api to avoid exporting) ----------
// The originals live in internal/api/users.go as unexported lowercase
// helpers. Duplicated here because exporting them for a CLI-only caller
// would widen the API surface for one consumer — copy is cheaper.

var cliUsernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func cliValidUsername(s string) bool { return cliUsernameRe.MatchString(s) }

func cliLinuxUserFromEmail(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return ""
}
