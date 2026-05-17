# Jabali Panel — Domain Context

Domain language for the hosting-control-plane. Seeded 2026-05-16 during an
architecture-deepening review; extend it as deepened modules name concepts.

## Language

### Control plane

**Agent**:
The root-privileged process that performs every privileged host
operation, reached over a Unix socket. Callers never touch the host
directly.
_Avoid_: worker, daemon, helper

**Reconciler**:
The in-process loop that converges database state onto host state
(timers, vhosts, config). The only thing that may *re-apply* derived
state on a schedule.
_Avoid_: sync job, cron, poller

**Privileged DB Admin Action**:
An operator-initiated database-engine operation (root/superuser
password, config tune, maintenance, processlist/kill, admin SSO) that
MUST be agent-dispatched, audited (success *and* failure), and
announced. The audit + announcement are part of the action, not the
caller's afterthought.
_Avoid_: db endpoint, admin handler, db op

### Cron

**Cron Job**:
A user's scheduled command (5-field schedule + allowlisted command),
owned by a user who must have a Linux account.
_Avoid_: task, timer, schedule entry

**Cron Job Intake**:
The single validated path that turns a create/update request into a
persisted, applied **Cron Job**: name+linux-account+schedule+command
validation → owned-docroot resolution → persist → **Agent** apply.
Identical regardless of whether the caller is the REST API or the CLI.
_Avoid_: cron create, cron pipeline, add-cron flow

### Database SSO

**SSO Token**:
A single-use, short-TTL token a user redeems to land authenticated in
a database web UI (phpMyAdmin / Adminer).
_Avoid_: sso cookie, login token

**Shadow Account**:
The engine-side account an **SSO Token** authenticates as — either a
per-DB-user scoped account or the all-databases privileged admin
account.
_Avoid_: proxy user, sso user

**SSO Token Resolution**:
The single security-critical decision that turns a consumed **SSO
Token** into engine **Credentials**: admin-scope (`__M46_ADMIN_ALL__`
sentinel → privileged **Shadow Account**) vs per-DB-user (ownership
check → scoped **Shadow Account**). Engine wire-shape formatting is
*not* part of resolution.
_Avoid_: validate handler, sso lookup

## Relationships

- A **Cron Job** belongs to a user who has a Linux account; **Cron Job
  Intake** is the only producer of one.
- **Cron Job Intake** and every **Privileged DB Admin Action** dispatch
  through the **Agent**; only the **Reconciler** re-applies them later.
- An **SSO Token** resolves (via **SSO Token Resolution**) to exactly
  one **Shadow Account**'s **Credentials**.

## Example dialogue

> **Dev:** "If `jabali cron add` and the REST create disagree on
> whether the user needs a Linux account, which is right?"
> **Domain expert:** "Neither is 'right' — there is one **Cron Job
> Intake**. Both are adapters over it. A disagreement is a missing
> seam, not a policy choice."

## Flagged ambiguities

- "validate handler" was used for both the SSO endpoint and **SSO Token
  Resolution** — resolved: the handler is an adapter; resolution is the
  module behind the seam.
- "apply" for cron was ambiguous: REST applied synchronously, CLI
  deferred to the **Reconciler**. Resolved 2026-05-16 — **Cron Job
  Intake** owns synchronous **Agent** apply for both callers; the
  **Reconciler** only re-applies for drift.
