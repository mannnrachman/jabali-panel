# ADR-0044: M6 IMAP migration — deferred to M15, manual escape hatch for day-one operators

**Date**: 2026-04-21
**Status**: accepted
**Deciders**: shuki + Claude
**Related**: ADR-0041 (Bulwark webmail), plan §1 decision 5, M15 (migration importers, planned)

## Context

Operators onboarding to jabali-panel from another hosting provider typically want to bring mailboxes with them. Stalwart v0.16.0 ships `stalwart-cli` with an IMAP sync sub-command that pulls remote mailboxes into the local store, which is sufficient for small migrations run manually. A UI-driven, multi-mailbox, progress-tracked, rollback-safe bulk importer is substantially more work.

M15 (migration importers) is the planned milestone for cross-control-panel migration — cPanel, DirectAdmin, HestiaCP, WHM — and mail is one of several domains that milestone will cover. Folding a bespoke mail-only importer into M6 would:

- Duplicate plumbing that M15 will build anyway (job tracking, progress UI, rollback, per-user auth handling).
- Pre-commit us to one migration shape (remote IMAP pull) before the broader M15 architecture is designed.
- Expand M6's scope by roughly a full step's worth of work.

## Decision

**IMAP migration is out of scope for M6.** Day-one operators who need to move mailboxes from an external IMAP server use `stalwart-cli` directly against the local Stalwart instance. Plan Step 9's runbook documents the recipe as an operator-facing escape hatch.

The recipe points at upstream Stalwart documentation for the exact CLI flags — we don't pin flag syntax in the ADR or the runbook because the CLI surface is upstream-owned and may evolve between v0.16.x patch releases.

When M15 ships, the panel will add a UI-driven flow that wraps `stalwart-cli` (or whatever Stalwart's preferred API is at that time) with:

- Credential capture + temporary encryption at rest.
- Job tracking in a `migration_jobs` table (M15's own migration scope).
- Per-mailbox progress + rollback.
- Integration with the wider M15 import pipeline (domains, DNS, database, WordPress — mail is one of many).

M6's `mailboxes` table is designed to not get in M15's way: a migrated mailbox is just an `INSERT` followed by `stalwart-cli` populating RocksDB. The schema doesn't need a `migrated_from` column or a migration-in-progress flag; M15 adds those to its own tables.

## Consequences

### Positive

- M6 ships a smaller, easier-to-reason-about surface.
- M15's designer gets full freedom to shape the migration UX without being cornered by M6 decisions.
- Day-one operators still have a path — it's just a CLI path, not a UI path.

### Negative

- Operator friction at day one. A user with 200 mailboxes to migrate will need a loop-over-mailboxes shell script. Runbook documents the shape.
- No panel-side progress feedback on in-flight migrations. Operators check `stalwart-cli`'s own output.
- The v1 CLI recipe is against a specific Stalwart version; it will drift. Runbook says "consult upstream `stalwart-cli --help imap-sync`" rather than pinning flags.

### Rejected alternatives

- **Fold imap-migrate into M6.** Costs a full step's worth of work; re-does plumbing M15 will build from scratch anyway.
- **Build a Bulwark-side importer.** Bulwark is an end-user webmail, not an admin import tool. Wrong layer.
- **Ship a third-party tool (imapsync)** and wrap it. Adds a second external dependency on top of Stalwart's own imap-sync; no clear upside.

## Related

- ADR-0041 — Bulwark is the webmail; Bulwark does not do migrations.
- Plan: `plans/m6-email-stalwart.md` §1 decision 5, Step 9 runbook.
- M15 — migration importers milestone, planned per BLUEPRINT.
- Stalwart `stalwart-cli` — upstream tool at `github.com/stalwartlabs/stalwart`.
