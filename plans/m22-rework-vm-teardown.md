# M22 Rework — Test VM Teardown (192.168.100.13)

**Status**: Cleanup runbook for the existing test VM that ran the original M22 magic-link design.
**Applies to**: any host that had M22 (mu-plugin + HMAC-callback) deployed before the 2026-04-21 rework.
**Order matters**: deploy first, migrate second, clean up orphaned files third.

## Why this exists

The original M22 design left durable artefacts on disk: an HMAC signing key, a canonical mu-plugin source tree, and per-install mu-plugin copies under each WordPress install's `wp-content/mu-plugins/`. The rework no longer uses any of them. The new design works whether they're present or absent (the rework removes all code paths that read them), but they're stale and worth removing.

Steps below are ordered: deploy → migrate (automatic via golden migration on deploy) → cleanup → verify.

## Steps

### 1. Deploy the rework

```bash
ssh -p 2222 root@192.168.100.13
cd /usr/local/lib/jabali  # or wherever the install lives
jabali update
```

This pulls the post-rework binaries (no `magiclink` package, no boot-time key load), runs migration `000053` (`DROP TABLE IF EXISTS magic_link_tokens`), and installs the new `jabali-sso-reaper.{service,timer}` units (idempotent; `update.go` calls `systemctl enable --now jabali-sso-reaper.timer`).

### 2. Verify clean boot

```bash
journalctl -u jabali-panel --since "5 min ago" | grep -iE 'magic.link|sso' | head
```

Expected output: nothing matching `magic-link` or related load errors. The reaper subcommand may log `sso-reap: scanned=N deleted=M paths=K` after its first sweep — that's normal.

If the panel logs `magic-link key load failed` or `failed to open /etc/jabali-panel/magic-link.key`, the rework didn't deploy. Re-run `jabali update`.

### 3. Remove the HMAC key

```bash
rm -f /etc/jabali-panel/magic-link.key
```

The file was mode `0600 root:jabali`. `rm` is sufficient — no other processes hold it open after `jabali update` restarts the panel.

### 4. Remove the canonical mu-plugin source

```bash
rm -rf /usr/local/lib/jabali/wp-mu-plugins/
```

This is the source tree `install_jabali_wp_mu_plugin` used to copy from. With the rework, the function and the install.sh call site are both deleted, so nothing references this directory.

### 5. Remove per-install mu-plugin copies

Each WordPress install created with the original M22 has a copy of the mu-plugin at `<webroot>/wp-content/mu-plugins/jabali-magic-link.php`. Find and delete them:

```bash
find /home -path '*/wp-content/mu-plugins/jabali-magic-link.php' -delete
```

Use `-print` first if you want to see which installs are affected before deleting:

```bash
find /home -path '*/wp-content/mu-plugins/jabali-magic-link.php' -print
```

WP installs that didn't have M22 deployed (anything pre-M22-original or installed under a different home prefix) are unaffected.

### 6. Optional: clean dead config line

The `MagicLinkKeyPath` config field is gone in the rework. BurntSushi TOML silently ignores unknown fields (rework plan §"Migration ordering note"), so leaving the line in `/etc/jabali/config.toml` is harmless. Hygienic cleanup:

```bash
sed -i '/magic_link_key_path/d' /etc/jabali/config.toml
```

### 7. Verify the reaper timer is active

```bash
systemctl status jabali-sso-reaper.timer
```

Expected: `active (waiting)` with a `Trigger:` timestamp in the next 30 seconds (or 60s if the host just booted). If the timer is `inactive`, run:

```bash
systemctl daemon-reload
systemctl enable --now jabali-sso-reaper.timer
```

This shouldn't be necessary — `jabali update`'s "sync systemd + shims" step does it idempotently — but it's the manual fallback.

### 8. Smoke test

In the panel UI:

1. Navigate to Applications.
2. Find a `Ready` WordPress install row.
3. Click "Log in to admin".
4. Verify a new tab opens at `https://<site>[/<subdir>]/jabali-sso-<43chars>.php`.
5. Verify it redirects to `/wp-admin` and you're signed in as the install's admin user.

After ~90s, verify the file is gone:

```bash
ssh -p 2222 root@192.168.100.13
find /home -name 'jabali-sso-*.php' -mmin +2
```

Expected: no output. If a file lingers past 2 minutes, the reaper isn't sweeping (see step 7).

## Rollback

The rework is designed to deploy cleanly. If step 1 fails (panic on boot, migration fails), the rollback is a normal package downgrade:

```bash
# revert to the pre-rework binary (whatever the last main commit before m22r/wave-d merge was)
git -C /usr/local/lib/jabali checkout <pre-rework-sha>
jabali update --no-pull   # or rebuild from local source
migrate down 1            # restores the magic_link_tokens schema (data is gone)
```

The mu-plugin source + key are still on disk if you didn't run steps 3–5 yet — that's the entire point of the deploy-first ordering.

## Post-teardown state

After all 8 steps complete on `192.168.100.13`:

- `/etc/jabali-panel/magic-link.key` does not exist.
- `/usr/local/lib/jabali/wp-mu-plugins/` does not exist.
- No WP install has `wp-content/mu-plugins/jabali-magic-link.php`.
- `magic_link_tokens` table does not exist.
- `jabali-sso-reaper.timer` is `active (waiting)`.
- `jabali-sso-reaper.service` shows recent successful runs in `journalctl`.
- "Log in to admin" works end-to-end on a Ready WP install.
