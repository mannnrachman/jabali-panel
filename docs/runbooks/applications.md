# Applications framework — operator runbook

Covers the M19 Applications surface (steps 1–5 + 8 shipped 2026-04-19).
Read **ADR-0033** for the architectural decision and the eight-step
plan at `plans/m19-applications-framework.md` for the construction
detail.

## Quick reference

| What | Where |
|---|---|
| UI | `/jabali-panel/applications` (user) and `/jabali-admin/applications` (admin) |
| API (M19) | `POST/GET /api/v1/applications`, `GET /api/v1/applications/:id`, `DELETE /api/v1/applications/:id`, `POST /api/v1/applications/:id/clone`, `GET /api/v1/applications/registry` |
| API (legacy, deprecated) | `POST/GET /api/v1/wordpress-installs`, …/:id, …/:id/clone — same handlers, removed in M19.1 |
| Agent commands (M19) | `app.install`, `app.delete`, `app.clone` — dispatcher in `panel-agent/internal/commands/app_dispatch.go` |
| Agent commands (legacy) | `wordpress.install`, `wordpress.delete`, `wordpress.clone` — same handlers, removed in M19.1 |
| App descriptors | `panel-api/internal/apps/<name>.go`, registered via `apps.RegisterDefaults` in `panel-api/internal/app/app.go` |
| Wire contract | `panel-api/internal/agent/app_contract_test.go` + `panel-api/internal/agent/testdata/app_*.json` |
| Database table | `application_installs` (renamed from `wordpress_installs` in migration 000046); composite UNIQUE on `(domain_id, subdirectory, app_type)` |

## Adding a new app

Two-file change on the panel + two-file change on the agent. Walk
through it once and the next app takes ten minutes.

### 1. Write the descriptor

Create `panel-api/internal/apps/<name>.go`:

```go
package apps

var YourApp = App{
    Name:                 "yourapp",      // matches app_type column
    DisplayName:          "Your App",
    Icon:                 "AppstoreOutlined",
    Description:          "One-line tagline shown in the picker.",
    DefaultSubdirectory:  "",             // or e.g. "shop"
    RequiresDB:           true,           // false skips the entire DB chain
    AgentInstallCmd:      "app.install",  // always app.* in M19
    AgentDeleteCmd:       "app.delete",
    AgentCloneCmd:        "",             // empty = Clone button hidden
    InstallParamSchema: map[string]ParamSpec{
        "site_title":     {Type: "string", Required: true},
        "admin_username": {Type: "string", Required: true},
        "admin_email":    {Type: "email",  Required: true},
        "admin_password": {Type: "password", Required: false},
    },
}
```

Then opt in by adding one line to `RegisterDefaults` in
`panel-api/internal/apps/wordpress.go`:

```go
if err := r.Register(YourApp); err != nil {
    return err
}
```

### 2. Write the agent installer

Create `panel-agent/internal/commands/yourapp_install.go`:

```go
package commands

import (
    "context"
    "encoding/json"
    "git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

type yourappInstallReq struct {
    AppType      string `json:"app_type"`     // present, ignore
    OSUser       string `json:"os_user"`
    Docroot      string `json:"docroot"`
    Subdirectory string `json:"subdirectory"`
    SiteURL      string `json:"site_url"`
    AdminUser    string `json:"admin_user"`
    AdminPass    string `json:"admin_pass"`
    AdminEmail   string `json:"admin_email"`
    // …per-app fields read out of the params map the panel sent
}

type yourappInstallResp struct {
    Version string `json:"version"`
}

func yourappInstallHandler(ctx context.Context, params json.RawMessage) (any, error) {
    var req yourappInstallReq
    if err := json.Unmarshal(params, &req); err != nil {
        return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: err.Error()}
    }
    // …download, extract under systemd-run --uid=<user> --slice=jabali-user-<user>.slice,
    // write config files, call wp-cli/maintenance/install.php/etc.
    return yourappInstallResp{Version: "1.0.0"}, nil
}

func init() {
    RegisterAppInstaller("yourapp", yourappInstallHandler)
}
```

Same shape for `yourapp_delete.go` (registers via `RegisterAppDeleter`)
and optionally `yourapp_clone.go`.

### 3. (Optional) Cross-boundary contract fixtures

If the request shape is non-trivial, drop a fixture pair under
`panel-api/internal/agent/testdata/app_<verb>_<name>_request.json` /
`_response.json` and add round-trip tests to
`panel-api/internal/agent/app_contract_test.go`. The pattern is
identical to the WordPress fixtures already there. This catches the
JSON-tag drift class per `feedback_cross_boundary_contracts`.

### 4. UI

Today the UI surfaces the app in the install-picker dropdown
automatically (the modal pulls `GET /applications/registry`), but
the per-app form fields are still WordPress-shaped. Folding the form
renderer over the descriptor's `InstallParamSchema` is the Step 5
follow-up that Step 6 (DokuWiki) blocks on. Until that lands, your
new app's modal will collect the WP-shaped fields; map them in the
agent installer or wait for the dynamic renderer.

---

## Common failure modes

### `400 invalid_app_type`

The panel posted to `/applications` with an `app_type` that isn't
in the registry. Causes:

- The descriptor file isn't being imported (no symbol referenced from
  outside the file). Make sure `RegisterDefaults` adds the descriptor.
- Server hasn't been restarted after deploying a new descriptor —
  `apps.New()` runs once at startup.
- The UI fetched a stale registry. Hard-refresh the install modal.

### `400 invalid_params: missing required param "x"`

The panel sent a `params` map missing a `Required: true` entry from
the descriptor's `InstallParamSchema`. Either:

- The UI form is missing the field. Add it to the install modal or
  defer to the dynamic renderer (Step 5 follow-up).
- The panel rolled back to a pre-descriptor version. Confirm the
  `app_type` registry on `GET /applications/registry` and the
  descriptor in code.

### `409 install_exists`

Two installs of the same `app_type` cannot share the same
`(domain_id, subdirectory)` slot — the migration-000046 composite
UNIQUE enforces it at the DB layer; the API does an explicit lookup
first to surface the friendly 409. Two **different** `app_type`s
**can** share a slot (a WordPress at `/` and a DokuWiki at `/wiki`
share the same domain — that's the design). Surface of the error is
the install modal's "Folder" field.

### Install row stuck in `installing`/`cloning`/`deleting`

The reconciler sweeps stuck rows after 10/30/5 minute timeouts
(configurable via `WORDPRESS_INSTALL_TIMEOUT` /
`WORDPRESS_CLONE_TIMEOUT` / `WORDPRESS_DELETE_TIMEOUT`). Names are
`WORDPRESS_*` from the M10 era — they cover all app types now.

- Check `last_error` in the row: `mysql -e "SELECT id, app_type,
  status, last_error FROM jabali_panel.application_installs WHERE
  status NOT IN ('ready','failed') AND updated_at < NOW() - INTERVAL
  10 MINUTE;"`.
- Tail the agent: `journalctl -u jabali-agent -f` and look for the
  matching `app.install` / `app.delete` / `app.clone` line.
- Manual recovery: flip the row to `failed` to clear the lock, then
  let the user delete + retry from the UI.

### Agent logs show `unknown app_type "X" for app.install`

The dispatcher accepted the request but no handler is registered for
that `app_type`. Either:

- The agent binary is older than the panel (panel knows about an app
  the agent doesn't). Redeploy the agent.
- The handler file's `init()` doesn't call `RegisterAppInstaller`.
  Check the file.

### Legacy `wordpress.install` showing up in agent logs

Expected through M19. The panel was rolled back to a pre-Step-4
build, or some external tooling is calling the legacy command
directly. Both still work because the legacy `Default.Register(
"wordpress.install", …)` line is intact. M19.1 deletes it; verify
no straggler caller before that PR ships.

---

## Where things live on disk

Per install, regardless of `app_type`:

| Path | Owner | What |
|---|---|---|
| `/home/<user>/domains/<domain>/public_html/[<subdir>/]` | `<user>:www-data` | App docroot |
| `/etc/jabali-panel/fpm/pool.d/jabali-<user>.conf` | root | FPM pool (per-user M9.5) |
| `/etc/nginx/sites-enabled/<domain>.conf` | root | Vhost (per M2 + M9.6 + SSL ADRs) |

For RequiresDB=true apps:

| Path | Owner | What |
|---|---|---|
| MariaDB `<user>_<app>_<6char>` | MariaDB | App's database |
| MariaDB user `<user>_<app>_<6char>` | MariaDB | App's DB user |

To back up an install end-to-end:

```bash
# Files
sudo -u <user> tar czf /tmp/<domain>-<app>.tgz \
    -C /home/<user>/domains/<domain>/public_html [<subdir>/]

# DB (if applicable)
mysqldump --single-transaction <user>_<app>_<6char> > /tmp/<domain>-<app>.sql
```

---

## Migration recovery (000046 dirty)

If migration 000046 fails halfway through (rare; the migration is
RENAME TABLE + ADD COLUMN + ADD UNIQUE + DROP INDEX in FK-safe
order), `schema_migrations` will read `version=46, dirty=1`. Recover:

```bash
mysql -u root -e "SELECT version, dirty FROM jabali_panel.schema_migrations;"
# Inspect application_installs vs wordpress_installs to determine
# how far the migration got, finish the missing ALTERs by hand,
# then:
mysql -u root -e "UPDATE jabali_panel.schema_migrations SET dirty=0;"
systemctl restart jabali-panel
```

The down migration intentionally **refuses** if any row has
`app_type != 'wordpress'`. Once the first DokuWiki/MediaWiki install
exists the rollback path is one-way; the recovery is forward — fix
the failing migration, don't roll back.

---

## Reference

- **ADR-0033** — design decisions, why one table + registry over
  per-app tables, why PHP-only for v1.
- **plans/m19-applications-framework.md** — the eight-step
  construction plan.
- **panel-api/internal/apps/wordpress.go** — example descriptor.
- **panel-agent/internal/commands/wordpress_install.go** — example
  agent installer.
- **panel-api/internal/agent/app_contract_test.go** — the
  cross-boundary contract test pattern.
