# Tech-debt — Single-source the jabali-appsec.yaml template

**Status:** Blueprint (pre-advisor)
**Type:** Consolidation refactor (ADR-0083 shape) — amends ADR-0102; no new milestone #
**Trigger:** ADR-0102 follow-up, recorded during the `/diagnose` AppSec fix.

## 1. Problem

`/etc/crowdsec/appsec-configs/jabali-appsec.yaml` is hand-authored by
**two** writers that must stay byte-compatible:

1. `install.sh install_crowdsec_appsec` — bash heredoc/printf
   (fresh-write + reconcile + the ADR-0102 idempotent `on_match`
   ensure).
2. `panel-agent security_crowdsec.go` `csAppSecGeoblockSetHandler` —
   Go string template (`header` + per-mode `pre_eval`), FULLY
   regenerates the file on every geoblock Apply.

This is the exact `feedback_cross_boundary_contracts` drift class.
ADR-0102 already had to patch the SAME `on_match` block into both;
the next change (a new inband rule, an exclusion tweak, a header
field) will silently diverge — and the agent's full-regenerate
**wins**, so a divergence = the file the operator actually runs is
the agent's, install.sh's intent lost on the first geoblock toggle.

Deletion test: delete either writer's template → the appsec-config
schema/content reappears in the other and must be re-duplicated for
any third producer. Concentrates → real seam missing.

## 2. Decision (to confirm with advisor)

One canonical renderer; both producers call it.

- New repo-root `internal/appseccfg` (ADR-0083 sibling of
  `cronvalidate`/`dbtuning`/`ssoadmin`): pure function
  `Render(opts) string` emitting the full `jabali-appsec.yaml`
  (header w/ jabali-mode/countries, presence-gated `inband_rules`,
  the ADR-0102 `/api/v1/admin/` `on_match` allowlist, per-mode
  geoblock `pre_eval`). Single source of the schema + the allowlist.
- `panel-agent security_crowdsec.go` `csAppSecGeoblockSetHandler`
  calls `appseccfg.Render` instead of its inline `header`/`switch`.
- `install.sh` cannot import Go → add a thin `jabali appsec
  render-config` cobra subcommand (panel-api binary, which install.sh
  already invokes) that calls `appseccfg.Render` and writes the file
  with the right perms; install.sh replaces its bash heredoc +
  reconcile + idempotent-ensure with one call to that subcommand
  (presence-gated inband detection moves into Go: it stat()s
  `/etc/crowdsec/appsec-rules/*`).
- Reconcile semantics preserved: the subcommand is idempotent, keeps
  the operator header (jabali-mode/countries) + any agent-injected
  geoblock state when invoked in "reconcile" mode (read current →
  re-render preserving mode/countries).

Net: the `on_match` allowlist + the whole appsec-config schema live
in ONE Go function with ONE test; bash carries zero appsec YAML.

## 3. Steps (5, inline)

1. `internal/appseccfg/appseccfg.go` + table test: `Render(opts)` —
   opts{Mode, Countries, InbandRules, AdminAllowlist=true}. Test
   pins: contains the ADR-0102 `on_match` allow block; off-mode = no
   `pre_eval`; allow/deny modes = correct geoblock `pre_eval`;
   inband list reflects opts. RED→GREEN.
2. Rewire `security_crowdsec.go` geoblock handler → `appseccfg.Render`;
   delete the inline `header`/`switch` template. Agent build + the
   existing geoblock tests green (behavior identical — assert the
   rendered bytes match the old template for each mode in a golden
   test before deleting).
3. `jabali appsec render-config [--reconcile]` cobra subcommand
   (cmd/server) → `appseccfg.Render`, writes
   `/etc/crowdsec/appsec-configs/jabali-appsec.yaml` 0644 root:root,
   presence-gates inband by stat'ing the rules dir. Reconcile mode
   reads current header (mode/countries) and preserves it.
4. `install.sh install_crowdsec_appsec`: replace the heredoc +
   reconcile awk + ADR-0102 idempotent-ensure with a single
   `jabali appsec render-config --reconcile` call (after the
   panel binary is built/installed; ordering per
   `feedback_install_function_repo_dependency`). `bash -n` + the
   `tools/test-appsec-crs-inband.sh` harness.
5. `.150` verify (the only real test for this): post-change,
   `jabali-appsec.yaml` byte-identical to the known-good (golden) for
   mode=off; admin config `PUT` still passes the WAF (ADR-0102 intact);
   geoblock toggle still works and KEEPS the `on_match` (the bug this
   prevents). Amend ADR-0102 (follow-up closed) + memory.

## 4. Scars honored

ADR-0083 single-source shape; golden test before deleting a template
(behavior-identical proof); install.sh fn ordering vs repo build
(`feedback_install_function_repo_dependency`); `.150` is the real
AppSec safety net (unit can't run CrowdSec); branch-only; inline.

## 5. Risk

Low-medium. Main risk = the reconcile/preserve semantics (operator
header jabali-mode/countries + agent geoblock state) must survive the
Go rewrite exactly — the golden test (step 2/5) is the guard.
Sequencing in install.sh: render-config call MUST be after the
panel binary is in place (it IS the renderer).
