# M36 — Per-domain ACL (POSIX facl) management

**Goal.** Operator/user can grant additional read/write/execute access on a
domain's document root (and named subtrees) to specific Linux groups or
other users, beyond the default `<owner>:www-data 0640` model.

Use case from old issue tracker (#108): a hosting team wants a
"deploy" group that can write to `/home/<user>/public_html/<domain>/`
without being given the full account password; or two related accounts
(parent + sub-account) that share write access to a specific subdir.

Today jabali2 has no facl knobs — every per-domain dir is fixed at
the M9 / M19 default `<user>:www-data` ownership.

Branch: `m36/per-domain-facl`. Default mode: branch + ff-merge into
`main` after every step.

ADR target: **0080** (still free at time of plan refresh 2026-04-29;
M34 took 0084 and M35 will take 0085, so 0080 is unblocked).

Migration high-water-mark on main: 000102 (post-M34). M36 takes the
next free contiguous range at dispatch time; if M35 (or any other
milestone with migrations) ships first, M36 renumbers off the new
high-water.

**MariaDB FK collation requirement:** every CREATE TABLE Step 1 introduces
that has a FOREIGN KEY back to `users(id)` or `domains(id)` MUST declare
both `DEFAULT CHARSET=utf8mb4` AND `COLLATE=utf8mb4_unicode_ci`, AND
ULID columns referencing those tables MUST be `CHAR(26)` (not
`VARCHAR(26)`). M34 scar; ref b336d856 + 10569464.

## Constraints + invariants

- **POSIX getfacl/setfacl on ext4/xfs.** No NFSv4 ACL, no ZFS POSIX
  layer. install.sh already requires `acl` package + `mount -o acl`
  on ext4. xfs has facl on by default.
- **Recursion is opt-in.** A grant on `/home/<u>/public_html/foo/`
  applies to that dir only by default. The form gets a "Apply
  recursively + set default ACL" checkbox; default ACL is what
  newly-created subfiles inherit.
- **Subjects are jabali users + system groups.** No raw uid/gid input.
  The grant form lists existing jabali users and existing Linux
  groups under a fixed prefix (e.g. `jabali-deploy-*`); ad-hoc
  groups are NOT created automatically by this milestone.
- **www-data + the owning user are RESERVED.** Operator can grant
  others additional access on top, but cannot revoke www-data's
  read/exec or the owner's full access via this UI. (Enforce in API
  validation; setfacl --remove of those entries breaks site
  serving.)
- **Reconciler does NOT manage facls.** This is operator-driven
  state. The reconciler reads the table once per tick to detect
  drift (someone ran setfacl by hand on the host); if drift exists,
  log a warning, do NOT auto-revert. Drift resolution = operator
  action via UI.
- **Per-domain only in v1.** Per-mailbox / per-database / per-cron
  ACLs are out of scope. The model is: one `domain_acls` row per
  (domain_id, target_path, subject_kind, subject_ref).
- **Audit log.** Every grant + revoke writes to `audit_log` with
  who/when/path/subject/perms.

## Steps

### Step 1: foundation — DB schema, model, repo, ADR-0080

**Files:**
- `panel-api/internal/db/migrations/0000NN_create_domain_acls.{up,down}.sql`
- `panel-api/internal/models/domain_acl.go`
- `panel-api/internal/repository/domain_acl_repository.go`
- `docs/adr/0080-per-domain-facl.md`

`domain_acls`:
```sql
CREATE TABLE domain_acls (
  id            CHAR(26) NOT NULL,
  domain_id     CHAR(26) NOT NULL,
  target_path   VARCHAR(512) NOT NULL,        -- relative to docroot
  subject_kind  ENUM('user','group') NOT NULL,
  subject_ref   VARCHAR(64) NOT NULL,         -- jabali user_id or linux group name
  perms         VARCHAR(3) NOT NULL,          -- one of: r-x, rwx, rw-, r--
  recursive     BOOLEAN NOT NULL DEFAULT 0,
  default_acl   BOOLEAN NOT NULL DEFAULT 0,
  created_at    DATETIME(0) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_domain_acl (domain_id, target_path, subject_kind, subject_ref),
  KEY idx_domain_acl_domain (domain_id)
) ENGINE=InnoDB;
```

### Step 2: agent commands — apply / list / remove

`panel-agent/internal/commands/facl_apply.go`
(`facl.apply` / `facl.list` / `facl.remove`).

`facl.apply` runs (per row):
```
setfacl  -m  <subject_kind>:<subject_ref>:<perms>  <abs-path>
setfacl  -d -m ...    # if default_acl
setfacl  -R -m ...    # if recursive
```

`facl.list` runs `getfacl --skip-base <path>` and parses; used by the
drift detector + UI verification.

`facl.remove` runs `setfacl -x <kind>:<ref> <path>` (and `-R -x` if
recursive) plus default-ACL counterpart.

Path safety: agent rejects target_path containing `..` or starting with
`/`. The agent prefixes the user's docroot itself.

### Step 3: panel-api REST + reconciler drift check

**Files:**
- `panel-api/internal/api/domain_acls.go` — CRUD endpoints under
  `/api/v1/admin/domains/:id/acls` and `/api/v1/me/domains/:id/acls`.
- `panel-api/internal/reconciler/domain_acl_drift.go` — every reconciler
  tick, sample 5 random rows + run `facl.list` on the agent; mismatches
  emit `domain.acl_drift` notification (M14).

REST shape:
- `GET    /admin/domains/:id/acls` — list
- `POST   /admin/domains/:id/acls` — create (validates subject exists,
  perms in allowed set, target_path safe)
- `DELETE /admin/domains/:id/acls/:acl_id` — revoke
- `POST   /admin/domains/:id/acls/sync` — operator-triggered "apply
  every row in DB to disk now"

User-side endpoints `/me/domains/:id/acls/*` mirror admin endpoints
but filter by ownership.

### Step 4: panel-ui — Drawer + table

**Files:**
- `panel-ui/src/shells/admin/domains/DomainACLDrawer.tsx`
- `panel-ui/src/shells/admin/domains/DomainACLTab.tsx` (new tab in
  domain detail view)
- user-shell mirror: `panel-ui/src/shells/user/domains/...`

UI shape:
- Tab on domain detail page: table of existing ACLs.
- "+ Grant access" button → Drawer with: target path (text + path
  picker), subject kind (User|Group), subject (autocomplete
  populated by GET `/me/users` or system groups), perms (rwx grid
  checkboxes), recursive (yes/no), default-acl (yes/no).
- Per-row "Revoke" + "Verify on disk" actions.

### Step 5: runbook + E2E + memory entry

Runbook covers:
- mount-option requirement (`acl` on ext4)
- conflict resolution when `getfacl` shows entries the DB doesn't
  know about (drift)
- removing the LAST grant for a subject restores default
  `<user>:www-data` access — verify
- supported subject_ref formats per kind

E2E: create grant → curl as the granted user → assert read works;
revoke → re-curl → assert 403.

## Out of scope

- POSIX capabilities (setcap) management
- SELinux / AppArmor labels
- Per-mailbox / per-database / per-cron ACLs
- Auto-creation of new linux groups on grant
- ACL inheritance from parent domain to subdomain (v1: each domain
  manages its own ACL list independently)
