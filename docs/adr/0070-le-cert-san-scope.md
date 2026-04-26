# ADR-0070: Let's Encrypt SAN list scoped to auto-provisioned DNS only

**Date**: 2026-04-26
**Status**: accepted
**Deciders**: shukiv
**Supersedes**: amends ADR-0066 (panel cert SAN)

## Context

ADR-0066 (panel cert) and the M6.1 per-domain cert flow both shipped with extra SANs intended to cover Bulwark-on-mail and email-client autoconfig probes:

- Panel cert: `[panel-hostname, mail.<panel-hostname>]`
- Per-domain cert (when `EmailEnabled`): `[domain, www.domain, mail.<domain>, autoconfig.<domain>]`

On the first public VPS install (`mx.jabali-panel.com`, 2026-04-26), every issuance failed because:

- `mail.<panel-hostname>` has no DNS record — the operator's DNS provider hosts the panel zone, not pdns. LE returned `NXDOMAIN looking up A for mail.mx.jabali-panel.com`.
- `autoconfig.<user-domain>` has no DNS record — pdns auto-creates `mail.` (MX target) but not `autoconfig.` LE returned `NXDOMAIN looking up A for autoconfig.jabali.site`.

LE HTTP-01 fails the entire issuance if any SAN's challenge fails — so the apex hostnames couldn't issue either, and every fresh install crash-looped between `pending_acme` and `pending_acme_retry`.

## Decision

The SAN list of any cert the panel issues MUST only contain hostnames whose DNS records the panel itself auto-creates and confirms reachable. Concretely:

- **Panel cert**: SAN = `[panel-hostname]` only. Drop `mail.<panel-hostname>`.
- **Per-domain cert** (`EmailEnabled=true`): SAN = `[domain, www.domain, mail.<domain>]`. Drop `autoconfig.<domain>`.

Any optional SAN beyond this baseline must be guarded by either (a) automatic DNS provisioning at the panel layer that confirms the record propagated before `ssl.issue` fires, or (b) an explicit per-domain admin toggle that the operator flips after setting DNS by hand.

## Alternatives Considered

### A. Keep the SANs and accept fresh-install failures

- **Pros**: Bulwark on `mail.<panel-hostname>` would have a trusted cert as soon as the operator manually adds DNS.
- **Cons**: Every fresh install fails until DNS is set; operator gets no signal beyond `pending_acme_retry`. We caught this on a real install — failure mode is silent on day 1.
- **Why not**: Default behaviour must converge on a working cert; manual DNS shouldn't be a hidden prerequisite.

### B. Per-SAN fallback (drop a failing SAN, retry with the rest)

- **Pros**: Best-effort coverage without giving up on optional names.
- **Cons**: Requires parsing certbot's per-challenge result + driving `--cert-name` and `--expand` carefully; LE rate limits on the failing SAN domain push you into the staging environment for testing. Significant net-new code.
- **Why not**: Heavyweight for a one-line policy fix. Re-evaluate when DNS auto-provisioning lands.

### C. Auto-create the missing DNS records at install / domain-create time

- **Pros**: Restores the original SAN coverage without operator intervention.
- **Cons**: Panel's pdns owns the customer domain zone — adding `mail.<domain>` + `autoconfig.<domain>` A records is feasible. But the panel hostname zone isn't in pdns; we don't have credentials for the operator's DNS provider.
- **Why not**: Half-fix only. The autoconfig A record on customer zones is a tractable follow-up; the panel hostname is genuinely outside our control.

## Consequences

### Positive
- Fresh public-VPS installs converge on a trusted cert without manual DNS.
- Cert reconciler error surface shrinks — `NXDOMAIN` errors disappear from `last_error` modals.
- Drops a class of incident (the cert SAN list racing operator-side DNS configuration).

### Negative
- Bulwark webmail at `mail.<panel-hostname>` keeps serving the per-domain LE cert — covered transitively through the apex domain whose webmail vhost it shares — but does not have its own cert; the operator's "Webmail" link routes through the panel-hostname `/webmail` redirect (ADR-0048), so this is invisible in practice.
- Outlook + Thunderbird auto-configuration probes against `autoconfig.<domain>` continue to fail with cert errors. JMAP discovery via `.well-known/jmap` (already in the per-domain mail vhost) covers the modern path; classic Mozilla autoconfig requires manual setup until the DNS auto-provisioning side lands.

### Risks
- Risk: operator who relied on `mail.<panel-hostname>` for direct webmail bookmark hits a cert mismatch warning.
  Mitigation: panel-hostname `/webmail` redirect (ADR-0048) routes operators to the panel-primary domain's mail vhost, which has its own cert via the per-domain SAN flow.
- Risk: future regression — someone re-adds the SANs without re-introducing DNS auto-provisioning.
  Mitigation: comments at the call sites in `panel-api/internal/reconciler/{panel_certificate_reconciler,reconciler}.go` cite this ADR; the SAN list helper `sanHostnamesForDomain` documents the rule.
