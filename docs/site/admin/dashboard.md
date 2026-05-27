# Admin Dashboard

`/jabali-admin/dashboard`. First page after admin login.

## Cards

- **Hosting summary** — total users, total domains, total mailboxes, total databases.
- **Health summary** — green/amber/red rollup of every watched service. Click to jump to [Server Status](./server-status.md).
- **Recent activity** — last 10 audit rows across the panel.
- **Disk usage** — per-mount used / free / quota.
- **Bandwidth (this month)** — total transferred across all users + top 5 users by usage.
- **Version** — current panel version + commit SHA + "update available" badge if `origin/main` is ahead.
- **Cert expiry** — count of certs expiring in <14 days; click to [SSL Manager](./ssl-manager.md).

## Quick actions

Top-right toolbar: New User · New Domain · Run Backup Now · Open Server Status · Open Audit.

## Source

`panel-ui/src/shells/admin/Dashboard.tsx`. Data via `/api/v1/admin/dashboard/summary` (cached 30 s).

## Why no real-time stats

The dashboard is a daily-check surface, not a monitoring console — that's [Server Status](./server-status.md). The dashboard intentionally returns a cached summary so it loads under 200 ms even on busy hosts.
