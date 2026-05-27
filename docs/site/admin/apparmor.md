# AppArmor

Security → AppArmor. Per-profile status surface for the AppArmor profiles the panel ships.

## Shipped profiles

| Profile | Confines |
|---|---|
| `jabali-panel` | The panel API process. |
| `jabali-agent` | The privileged agent process. |
| `stalwart-mail` | Stalwart SMTP / IMAP / JMAP. |
| `pdns` | PowerDNS authoritative. |
| `pdns-recursor` | PowerDNS recursor. |
| `nginx` | The nginx master and workers. |
| `php-fpm` | Per-version FPM masters and pool workers. |
| `kratos` | Kratos identity. |
| `bulwark` | Bulwark Node SPA + bridge. |

Each profile is shipped under `/etc/apparmor.d/`. The installer enables them in `enforce` mode by default.

## States

For each profile, the page shows:

- **Mode** — `enforce`, `complain`, or `disabled`.
- **Denial count (24 h)** — incremented every time the kernel logs an AppArmor `DENIED` line for the profile.
- **Last denial** — the most recent denial detail (rule, path, syscall).

## Per-profile actions

- **Reload** — re-parse the profile from `/etc/apparmor.d/` and apply.
- **Set complain** — switches the profile to log denials without enforcing. Useful during incident response or after a host package upgrade introduces a transient denial pattern.
- **Set enforce** — restores enforcement.
- **Disable** — unload the profile entirely. Only do this temporarily.

## Diagnosing a denial

```bash
journalctl -k | grep DENIED
```

…shows kernel-side AppArmor denials with the profile, operation, and target. The most common cause of a new denial after `jabali update` is a path the agent now needs to touch (a new pool directory, a new state file) that is not in the shipped profile yet. File an issue; the fix lands in the next release.

## Custom rules

The shipped profiles include `tunables/global` and the local include `tunables/<profile>` for site-specific additions. Adding an `audit deny` rule in the local include is safe across `jabali update` — the agent does not overwrite the local includes.

## Why per-profile

A single global profile would either be too permissive (defeating the point) or too restrictive (denying legitimate operations the agent or panel needs). Per-process profiles let each component carry the narrowest possible policy.
