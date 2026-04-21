# ADR-0038: M16 Rollback — OIDC + Hydra dropped, magic-link (M22) replaces

**Date**: 2026-04-21
**Status**: accepted
**Deciders**: shuki + Claude
**Related**: ADR-0036 (M16 — now superseded), ADR-0034 (M20 Kratos identity), ADR-0039 (M22 magic-link design), [plans/m16-rollback-and-m22-magic-link.md](../../plans/m16-rollback-and-m22-magic-link.md)

## Context

M16 (ADR-0036) shipped a self-hosted Ory Hydra deployment on 2026-04-20 (Waves A–E merged) to provide OAuth 2 / OIDC federation for WordPress SSO. The operator validated the implementation end-to-end on a real VM and discovered a **UX-blocking incompatibility:**

**The Problem:**
The auto-installed WordPress plugin (`daggerhart-openid-connect-generic` v3.11.3 from WordPress.org) does not implement PKCE (Proof Key for Code Exchange). The panel's Hydra configuration enforces `pkce.enforced: true` per security best practices (PKCE is mandatory for native/SPA clients in RFC 6749 Section 4.1.1). When the plugin attempts to authenticate, Hydra rejects the flow with "PKCE required" → the user sees a **broken "Login with OpenID Connect" button** on every WordPress install.

**Root Cause Analysis:**
- The plugin is maintained by a small team and has never implemented PKCE despite the OAuth 2 spec.
- Relaxing PKCE in Hydra config (`pkce.enforced: false`) works around the symptoms but regresses the panel's security posture and normalizes non-PKCE clients.
- Swapping plugins would require either: (a) forking daggerhart and adding PKCE ourselves (ongoing maintenance), or (b) finding/vetting an alternative that may have other gaps.
- The actual UX goal — "click a button in the panel, land in wp-admin signed in" — does not require OAuth 2 or OIDC at all. It requires only a trusted, short-lived token exchange.

**Decision Rationale:**
The panel is a **single-operator self-hosted system**. Every WP install is on the same host as the panel. The panel already has cryptographic capabilities (bcrypt for local auth, AES-256-GCM for secrets). A simpler model — magic-link with signed tokens — achieves the goal without the federation overhead, without external OAuth 2 libraries, and without the PKCE/consent/token-endpoint complexity.

## Decision

**Full rollback of M16:**

1. **Remove the Hydra service** — systemd unit `jabali-hydra`, binary, config, SQLite state database all deleted.
2. **Revert all panel-side OIDC code** — `hydraclient` package, `oauth2_flow.go` handlers, consent SPA page, OIDC client minting in the apps framework, all deleted.
3. **Drop OIDC columns from the database** — migration 000051 removes `application_installs.oidc_client_id` + `oidc_client_secret_enc`.
4. **Revert WordPress plugin auto-install** — the agent no longer installs/configures OpenID Connect Generic.
5. **Archive M16 planning docs** — move `plans/m16-hydra-oauth.md` + `plans/m16-hydra-runbook.md` to `plans/archive/`.
6. **Mark ADR-0036 as superseded** by this ADR.
7. **Replace with M22 (magic-link)** — panel-side token minting + signature scheme, custom WP must-use plugin (signed token → `wp_set_auth_cookie()`), one-click admin login from the Applications UI.

## Consequences

### Positive
- **Simpler model** — signed token exchange replaces a full OAuth 2 / OIDC handshake. No PKCE, no consent screens, no token endpoints, no JWKS/key rotation complexity.
- **One fewer service** — Hydra systemd unit gone → simpler deployment, simpler monitoring, fewer moving parts for a single-operator system.
- **WordPress installs work immediately** — M22 plugin trades a signed token for `wp_set_auth_cookie()` without plugin config or network round-trips; the UX is click → logged in.
- **Generalizable to other apps** — the same signed-token pattern scales to any app with a login hook (Joomla, Drupal, etc.) without re-architecting for each app's OIDC support.
- **Retains the goal** — panel-side SSO is solved; the original M16 stretch goal (Automation API / machine tokens) is deferred to a fresh decision when it becomes urgent.

### Negative
- **OIDC federation is gone** — if we later need to expose the panel as a standards-compliant identity provider to external apps, we'll need to re-implement. However, single-operator self-hosted use cases don't require this today.
- **Wave F (Automation API)** — the original M16 plan used Hydra's `client_credentials` grant for machine tokens. This is deferred; a fresh ADR will address machine tokens when the need is clear (could still be Hydra, could be simpler signed-JWT tokens, or could be bearer tokens with a local DB).
- **Data loss on rollback** — Hydra's SQLite state (consent records, access tokens, refresh tokens) is ephemeral and non-recoverable. This is acceptable because: (a) existing tokens are short-lived and naturally expire, (b) users re-login once, (c) panel-side `application_installs` rows retain the material to re-register clients if needed.

### Risks
- **R1: Re-implementing OAuth 2 later** — if the panel ever needs federation (e.g., "use Google to log into Jabali"), Hydra becomes necessary again. Mitigation: the magic-link pattern is orthogonal to federation; M22 ships first, federation (if needed) lands on top.
- **R2: WP plugin CVEs in the future** — the OpenID Connect Generic plugin is no longer installed, so plugin-specific CVEs are irrelevant. The new M22 custom plugin carries its own maintenance burden but is only ~200 LOC (minimal attack surface).

## Rollback

The removal is clean because Hydra was entirely additive (no schema or configuration relied on it). Full rollback of M16 Steps 1–6 + the VM teardown (Step 7) restores a system where:
- Panel login stays on Kratos (unaffected).
- WordPress installs without OIDC see no change (never used it).
- WordPress installs with OIDC see SSO temporarily unavailable; fall back to classic `wp-login` (OpenID Connect Generic plugin's default behavior).
- All other apps remain unaffected.

## Acceptance Criteria

- [ ] ADR-0036 status updated to "superseded by ADR-0038 (2026-04-21)"
- [ ] M16 planning docs archived: `plans/archive/m16-hydra-oauth.md`, `plans/archive/m16-hydra-runbook.md`
- [ ] M16 code fully reverted: `hydraclient` package deleted, `oauth2_flow.go` deleted, consent SPA deleted, apps framework OIDC minting deleted, agent WordPress plugin install reverted
- [ ] Migration 000051 drops `application_installs.oidc_client_id` + `oidc_client_secret_enc`
- [ ] BLUEPRINT.md M16 section updated: "ROLLED BACK 2026-04-21" status, 3-line summary (what shipped, why rolled back, pointer to M22)
- [ ] VM teardown runbook created: `plans/m16-rollback-vm-teardown.md` with operator-facing commands, verification block, production-only checks
- [ ] `make test` green (Go + vitest)
- [ ] No grep hits for `hydra` (case-insensitive) outside comments and archived docs
- [ ] Fresh `install.sh` parses cleanly: `bash -n install.sh`

---

**Status change**: `proposed` → `accepted` on 2026-04-21 (Step 7 / documentation gate of the M16 rollback plan)
