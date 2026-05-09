# M44 — Automation API scoped tokens

**Status:** drafted 2026-05-09 · branch `m44/automation-tokens` (foundation
shipped as part of `feat/automation-tokens-foundation`)

**ADR target:** new **0093** — Automation API token shape, signature
algorithm, scope grammar.

**Cherry-picked from:** old PHP-era blueprint §4.15 (per
`project_old_blueprint_cherrypicks`).

## Why

External callers (CI scripts, monitoring systems, partner integrations)
need to hit a small read-only surface of /api/v1/* without:
- Holding a Kratos session (no browser)
- Storing user credentials (no machine-as-human)
- Going through OIDC (M16 was rolled back; no OAuth server)

A scoped, HMAC-signed token issued by an admin and revocable at any
time fills this gap. Per-token scopes constrain the blast radius: a
monitoring token gets `read:domains` only; a CI token might also get
`read:applications` + `write:applications.deploy`.

## Out-of-scope (now)

- Token rotation by automation (admin-only mint + revoke is enough
  for ship)
- Per-token rate limits beyond the existing per-IP middleware
- OAuth-style refresh tokens
- Per-tenant tokens (admin scope only; user-level automation = M44.1)

## Token shape

Header carried on every request:

```
Authorization: Jabali-HMAC kid=<token-id>, ts=<unix-seconds>, sig=<hex>
```

Where:
- `kid` = `automation_tokens.id` (CHAR(26) ULID)
- `ts` = unix epoch seconds; reject if more than 300s skew from server
  clock
- `sig` = `hex(HMAC_SHA256(secret, METHOD || "\n" || PATH || "\n" || ts || "\n" || sha256(BODY)))`
  - `METHOD` = uppercase HTTP method ("GET", "POST", …)
  - `PATH` = full request path including query string ("/api/v1/automation/domains?page=1")
  - `ts` = same value as in the header (decimal string)
  - `sha256(BODY)` = hex-encoded sha256 of the raw request body (empty
    string for GET → `e3b0c44…`)

Server stores `secret` as `secret_enc` — `models/ssokey.Seal` AES-GCM
ciphertext. Mint endpoint returns the plaintext ONCE (the admin UI shows
a "save it now" modal). Subsequent reads of the token list never expose
the plaintext.

## Scope grammar

`scopes` column is a JSON array of strings. Parser splits each on `:`:

| Scope            | Routes                           |
|------------------|----------------------------------|
| `read:domains`   | GET /api/v1/automation/domains   |
| `read:users`     | GET /api/v1/automation/users     |
| `read:applications` | GET /api/v1/automation/applications |
| `read:status`    | GET /api/v1/automation/status (server health summary) |

Wildcard scope `read:*` grants every read (mints in admin UI default).
`write:*` reserved for future use; rejected at mint time until an
explicit write route lands.

## Schema

```sql
CREATE TABLE automation_tokens (
    id              CHAR(26)        NOT NULL PRIMARY KEY,
    name            VARCHAR(100)    NOT NULL,
    scopes_json     JSON            NOT NULL,
    secret_enc      VARBINARY(255)  NOT NULL,
    created_by      CHAR(26)        NULL,         -- admin user_id; NULL after admin deletion
    created_at      DATETIME(6)     NOT NULL,
    last_used_at    DATETIME(6)     NULL,
    last_used_ip    VARCHAR(45)     NULL,
    revoked_at      DATETIME(6)     NULL,
    UNIQUE KEY uq_automation_tokens_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

## Caveman wave map

| Wave | Steps | Parallel? | Ship-ready exit |
|---|---|---|---|
| A | 1 → 2 | sequential | migration + repo + middleware land + unit-tested |
| B | 3 ‖ 4 | parallel | mint/list/revoke admin endpoints + first read-route mounted |
| C | 5 | sequential | admin UI list/create modal + one-time-secret reveal |
| D | 6 | sequential | ADR-0093 + runbook + memory + smoke harness |

## Step 1 — migration + model + repo

Mechanical. Already-shipped: foundation (`feat/automation-tokens-foundation`).
File list:
- `panel-api/internal/db/migrations/000116_automation_tokens.{up,down}.sql`
- `panel-api/internal/models/automation_token.go`
- `panel-api/internal/repository/automation_token_repository.go` (Mint,
  List, FindByID, Revoke, BumpLastUsed)
- Test fixtures backed by sqlmock as usual.

## Step 2 — HMAC middleware

`panel-api/internal/middleware/automation_hmac.go`:
- Parses `Authorization: Jabali-HMAC kid=…, ts=…, sig=…`.
- 5-minute clock skew window via `ts`.
- Looks up token by kid. 401 if not found / revoked.
- Decrypts secret via `ssokey.Open`.
- Recomputes signature from method/path/ts/body-hash.
- ConstantTimeCompare; 401 on mismatch.
- BumpLastUsed (best-effort goroutine; does not block request).
- Stash `*models.AutomationToken` in gin context for downstream
  scope check.

`panel-api/internal/middleware/automation_scope.go`:
- Reads token from gin context.
- Each registered route declares its required scope string.
- Rejects with 403 + JSON error when scope missing from token.scopes_json.

## Step 3 — Admin endpoints

Mounted at `/api/v1/admin/automation/tokens` behind RequireAdmin:
- `POST /` — mint. Body: `{name, scopes: [...]}`. Returns
  `{id, name, scopes, secret}`. **`secret` is plaintext, returned exactly
  once.** Subsequent reads omit it.
- `GET /` — list. Returns rows minus secret.
- `DELETE /:id` — revoke (sets `revoked_at` = now; soft delete keeps
  audit history).

## Step 4 — First read routes

Mount `/api/v1/automation/*` group behind:
1. `automation_hmac` middleware (proves authentication)
2. `automation_scope("read:domains")` per route

Initial route set:
- `GET /api/v1/automation/domains` — returns flat domain list (id,
  name, user_id, is_enabled, ssl_status). Strips listen-IP / DNS
  details so external callers don't accidentally cache sensitive infra
  topology.
- `GET /api/v1/automation/users` — returns flat user list (id,
  username, email, package_id, suspended).
- `GET /api/v1/automation/applications` — returns
  ApplicationInstall rows (id, app_type, domain, status).
- `GET /api/v1/automation/status` — returns
  `{panel_version, agent_version, healthy: bool, services: [...]}` —
  shrunk view of /admin/server-status sufficient for monitoring
  uptime checks.

## Step 5 — Admin UI

`panel-ui/src/shells/admin/automation/`:
- `AutomationTokensPage.tsx` — AntD Table with columns (Name, Scopes,
  Created, Last Used, Revoked). Row actions: Revoke
  (Popconfirm) using the new RowDeleteButton.
- `MintTokenDrawer.tsx` — Drawer form: name + scope checkboxes.
  On submit, opens a Modal with the plaintext secret + copy-to-clipboard;
  the Modal text says "this token is shown ONCE — copy it now or revoke
  + remint".

Sidebar entry under Admin → Automation (icon: KeyOutlined).

## Step 6 — Docs

- `docs/adr/0093-automation-api-tokens.md`.
- `plans/automation-api-tokens-runbook.md` — sample curl for each
  scope + signature script in bash + python.
- `docs/BLUEPRINT.md` — flip M44 row to Shipped after Wave D.
- Memory: `project_m44_shipped.md` linked from `MEMORY.md`.

## Notes

- **Don't store plaintext secrets** anywhere except the one-time-reveal
  modal in the operator's browser. Even logs must redact.
- **Don't accept HMAC-SHA1** for backward compat — SHA-256 from day one.
  Bump to a v2 algorithm later if needed.
- **Per-token IP allow-lists** are tempting; defer to v2 to keep the
  token shape tight. Operators that want IP gating use UFW or
  CrowdSec scenarios on the panel hostname + 80/443.
- ~~**No JTI / nonce store**~~ — **shipped 2026-05-09.** Replay
  defense added via Redis SETNX on `automation:replay:<kid>:<sig>`
  with TTL = clock-skew window + 1 min grace. `RequireAutomationHMAC`
  takes a `*redis.Client`; nil disables the gate (test/non-prod
  only). Production fail-closed: Redis-down returns 503, not 200.
