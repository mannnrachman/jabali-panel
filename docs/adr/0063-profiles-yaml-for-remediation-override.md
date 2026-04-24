# ADR-0063: Per-scenario remediation override via `/etc/crowdsec/profiles.yaml`

**Status:** Accepted — 2026-04-24
**Related:** ADR-0053 (CrowdSec over fail2ban), ADR-0060 (AppSec geoblock), ADR-0061 (allowlists), ADR-0062 (console)

## Context

CrowdSec's default profile issues a `ban` decision for every
actionable alert. That's right for SSH bruteforce, wrong for
http-bad-user-agent (lots of legit weird-UA traffic in the wild).
Operators have asked: "can I keep scanning-scenarios enabled but
have them issue a captcha challenge instead of a 403?"

CrowdSec solves this upstream through `/etc/crowdsec/profiles.yaml`
— a multi-document YAML file evaluated top-to-bottom with
`on_success: break`. The first profile whose `filters` expression
matches the alert decides the remediation. The shipped upstream
config has five catch-all profiles keyed on `Alert.GetScope()`.

Per-scenario override means: insert jabali-managed profiles BEFORE
the upstream defaults so they match first and specify a different
remediation (captcha or "off").

## Decision

Rewrite `/etc/crowdsec/profiles.yaml` using a marker-bounded block
at the TOP of the file. Reload via `systemctl reload crowdsec` after
pre-flight `crowdsec -t`.

### Why rewrite the whole file instead of a drop-in

CrowdSec's profile loader only reads `/etc/crowdsec/profiles.yaml`.
There's no `profiles.d/` include mechanism. Fragmenting profiles
across files isn't supported upstream.

Rewriting the whole file would clobber the upstream-shipped defaults.
Instead, we write a marker-bounded block at the top:

```yaml
# jabali-begin-overrides
# DO NOT HAND-EDIT — rewritten by jabali on Save. Edits inside these markers are lost.
# To add manual profiles, place them AFTER the jabali-end-overrides line below.
name: jabali-override-<scenario-sanitized>
filters:
  - Alert.Remediation == true && Alert.GetScenario() == "<scenario>"
decisions:
  - type: captcha
    duration: 4h
on_success: break
---
# jabali-end-overrides
```

Everything below `# jabali-end-overrides` is preserved byte-for-byte.

### Why marker comments not a separate config structure

Alternatives considered:
- **Separate sidecar file + symlink** — doesn't work, loader reads a
  single path
- **Jinja-style templating** — overkill, invites parse divergence
- **Delete whole file + rewrite from jabali template** — destroys
  operator customisations outside the jabali block

Markers keep the feature reversible. If M27 is rolled back, the
operator runs `sed -i '/# jabali-begin-overrides/,/# jabali-end-overrides/d' /etc/crowdsec/profiles.yaml`
and is back to upstream defaults.

### Pre-flight `crowdsec -t`

The M27 probe confirmed `crowdsec -t` exists and exits 0 on valid
config in v1.7.7. Agent:

1. Back up current profiles.yaml to `.bak`
2. Write the new file
3. Run `crowdsec -t`; if nonzero exit → restore `.bak`, return error
4. `systemctl reload crowdsec`; if reload fails → restore `.bak`,
   retry reload with old content, return error

Double safety net: pre-flight + restore-on-reload-failure.

### Wire contract

- `GET /admin/security/crowdsec/profiles` →
  ```json
  {
    "scenarios": [{"name", "description"}],
    "overrides":  [{"scenario", "action"}],
    "captcha_enabled": bool
  }
  ```
- `PUT /admin/security/crowdsec/profiles` body
  `{overrides: [{scenario, action: "captcha" | "off"}]}`

`action` values:
- `captcha` — issue captcha decision (requires M27 Step 5 `captcha_enabled`)
- `off` — skip remediation entirely (emit alert only, no decision)

Dropping back to default `ban` is expressed by REMOVING the override
from the list (not a third action value).

`captcha_enabled` is read from `server_settings.crowdsec_captcha_enabled`
(M27 Step 5). Panel-api rejects 400 if any override requests
`captcha` while `captcha_enabled=false`.

### Hand-edit contention

The marker block carries a warning comment. Any hand-edit inside the
markers is silently clobbered on the next Save. Operators who need
stable custom profiles place them OUTSIDE the markers.

## Consequences

### Good

- Operator-facing UI for the most common real-world tuning: "treat
  this scenario softer than a ban"
- Reversible rollback: delete the marker block
- Upstream default profiles preserved verbatim

### Neutral

- Requires one reload of CrowdSec per Save. Reload is SIGHUP, no
  connection loss

### Risks

- Hand-edit inside the markers is lost. Mitigation: warning comment
  in the block header; runbook entry
- Reload after bad YAML write corrupts LAPI config. Mitigation:
  `crowdsec -t` pre-flight + `.bak` restore
- profiles.yaml evaluation is ordered — a jabali "off" override
  higher up can mask a later profile. Acceptable: same "first match
  wins" behaviour already governs upstream config

## Implementation

- Agent handlers
  - `security.crowdsec.scenarios.list` — wrapped `cscli scenarios list -o json`
  - `security.crowdsec.profiles.get` — parse markers, return override list
  - `security.crowdsec.profiles.set` — write markers, pre-flight,
    reload, restore on failure
- Panel-api routes `GET/PUT /admin/security/crowdsec/profiles`
- UI card `ProfilesCard` — Table with inline `Action` Select per row
