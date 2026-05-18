// nav.ts — single source of truth for sidebar menu items.
//
// Replaces Refine's `resources={[...]}` config. Each shell owns its
// own list so an admin page can never accidentally leak into the user
// menu. The shape is small on purpose — anything page-specific
// (search behavior, row actions) lives in the page itself, not here.
//
// `path` is the absolute URL the item navigates to. `match` optionally
// lists longer paths that should still highlight this entry — useful
// when a nested route (e.g. /jabali-admin/users/create) should keep
// the "Users" row active.
import type { ReactNode } from "react";

import {
  AppstoreOutlined,
  BellOutlined,
  CalendarCheckOutlined,
  ChartBarOutlined,
  CloudServerOutlined,
  CodeOutlined,
  SquareTerminalOutlined,
  DownloadOutlined,
  FileTextOutlined,
  LifeBuoyOutlined,
  DatabaseOutlined,
  EthernetPortOutlined,
  FolderOutlined,
  GlobalOutlined,
  HomeOutlined,
  KeyOutlined,
  MailOutlined,
  PackageOpenOutlined,
  SafetyOutlined,
  SaveOutlined,
  ServerOutlined,
  SettingOutlined,
  ShieldCheckOutlined,
  SwapOutlined,
  TeamOutlined,
} from "@icons";
import { createElement } from "react";
import type { ComponentType } from "react";

export type NavItem = {
  key: string;
  label: string;
  icon: ReactNode;
  path: string;
  // matchPatterns lets an item claim deeper sub-paths it doesn't own
  // by `path` startsWith. Regex tested against the full pathname; if
  // any matches, the item is selected even when another item's path
  // would be a longer prefix. Use for nested routes like
  // /jabali-panel/domains/:id/dns that logically belong to a different
  // sidebar entry than the one /jabali-panel/domains owns.
  matchPatterns?: RegExp[];
};

// All sidebar icons render a shade larger than AntD's default (14px
// inherited from fontSize). 20px reads comfortably without crowding the
// label and keeps the collapsed-sider footprint tidy. Change here and
// every entry picks it up.
const NAV_ICON_SIZE = 20;

// Tailwind gray-500 (#6b7280) keeps the inactive icon row muted while
// the AntD-active item still gets its brand colour via the Menu's
// itemSelectedColor theme token (which overrides this inline color
// only for the selected key — `currentColor` inheritance kicks in
// on selection).
const NAV_ICON_COLOR = "#6b7280";

const navIcon = (Icon: ComponentType<{ style?: React.CSSProperties }>) =>
  createElement(Icon, { style: { fontSize: NAV_ICON_SIZE, color: NAV_ICON_COLOR } });

export const adminNav: NavItem[] = [
  {
    key: "dashboard",
    label: "Dashboard",
    icon: navIcon(HomeOutlined),
    path: "/jabali-admin/dashboard",
  },
  {
    key: "users",
    label: "Users",
    icon: navIcon(TeamOutlined),
    path: "/jabali-admin/users",
  },
  {
    key: "domains",
    label: "Domains",
    icon: navIcon(GlobalOutlined),
    path: "/jabali-admin/domains",
  },
  {
    key: "packages",
    label: "Hosting Packages",
    icon: navIcon(PackageOpenOutlined),
    path: "/jabali-admin/packages",
  },
  {
    key: "ssl",
    label: "SSL Manager",
    icon: navIcon(ShieldCheckOutlined),
    path: "/jabali-admin/ssl",
  },
  {
    key: "applications",
    label: "Applications",
    icon: navIcon(AppstoreOutlined),
    path: "/jabali-admin/applications",
  },
  {
    key: "logs",
    label: "Logs & Statistics",
    icon: navIcon(FileTextOutlined),
    path: "/jabali-admin/logs",
  },
  {
    key: "audit",
    label: "Audit Log",
    icon: navIcon(SafetyOutlined),
    path: "/jabali-admin/audit",
  },
  {
    key: "settings",
    label: "Server Settings",
    icon: navIcon(SettingOutlined),
    path: "/jabali-admin/settings",
  },
  {
    key: "server-status",
    label: "Server Status",
    icon: navIcon(ChartBarOutlined),
    path: "/jabali-admin/server-status",
  },
  {
    key: "security",
    label: "Security",
    icon: navIcon(SafetyOutlined),
    path: "/jabali-admin/security",
  },
  {
    key: "backups",
    label: "Backups",
    icon: navIcon(SaveOutlined),
    path: "/jabali-admin/backups",
  },
  {
    key: "php-pools",
    label: "PHP Manager",
    icon: navIcon(CodeOutlined),
    path: "/jabali-admin/php-pools",
  },
  {
    key: "dns",
    label: "DNS Zones",
    icon: navIcon(ServerOutlined),
    path: "/jabali-admin/dns",
    matchPatterns: [/^\/jabali-admin\/domains\/[^/]+\/dns(?:\/|$)/],
  },
  {
    key: "ips",
    label: "IP Addresses",
    icon: navIcon(EthernetPortOutlined),
    path: "/jabali-admin/ips",
  },
  {
    key: "notifications",
    label: "Notifications",
    icon: navIcon(BellOutlined),
    path: "/jabali-admin/notifications/channels",
  },
  {
    key: "updates",
    label: "Updates",
    icon: navIcon(DownloadOutlined),
    path: "/jabali-admin/updates",
  },
  {
    key: "support",
    label: "Support",
    icon: navIcon(LifeBuoyOutlined),
    path: "/jabali-admin/support",
  },
  {
    key: "automation",
    label: "Automation API",
    icon: navIcon(KeyOutlined),
    path: "/jabali-admin/automation",
  },
  {
    key: "migrations",
    label: "Migrations",
    icon: navIcon(SwapOutlined),
    path: "/jabali-admin/migrations",
  },
  {
    key: "terminal",
    label: "Terminal",
    icon: navIcon(SquareTerminalOutlined),
    path: "/jabali-admin/terminal",
  },
];

export const userNav: NavItem[] = [
  {
    key: "dashboard",
    label: "Dashboard",
    icon: navIcon(HomeOutlined),
    path: "/jabali-panel/dashboard",
  },
  {
    key: "domains",
    label: "Domains",
    icon: navIcon(GlobalOutlined),
    path: "/jabali-panel/domains",
  },
  {
    key: "mail",
    label: "Mail",
    icon: navIcon(MailOutlined),
    path: "/jabali-panel/mail/mailboxes",
  },
  {
    key: "applications",
    label: "Applications",
    icon: navIcon(AppstoreOutlined),
    path: "/jabali-panel/applications",
  },
  {
    key: "databases",
    label: "Databases",
    icon: navIcon(DatabaseOutlined),
    path: "/jabali-panel/databases",
  },
  {
    key: "files",
    label: "Files",
    icon: navIcon(FolderOutlined),
    path: "/jabali-panel/files",
  },
  {
    key: "logs",
    label: "Logs & Statistics",
    icon: navIcon(FileTextOutlined),
    path: "/jabali-panel/logs",
  },
  {
    key: "activity",
    label: "Account Activity",
    icon: navIcon(FileTextOutlined),
    path: "/jabali-panel/activity",
  },
  {
    key: "ssh-keys",
    label: "SSH Keys",
    icon: navIcon(KeyOutlined),
    path: "/jabali-panel/ssh-keys",
  },
  {
    key: "dns",
    label: "DNS",
    icon: navIcon(CloudServerOutlined),
    path: "/jabali-panel/dns",
    matchPatterns: [/^\/jabali-panel\/domains\/[^/]+\/dns(?:\/|$)/],
  },
  {
    key: "ssl",
    label: "SSL Manager",
    icon: navIcon(ShieldCheckOutlined),
    path: "/jabali-panel/ssl",
  },
  {
    key: "php-settings",
    label: "PHP Settings",
    icon: navIcon(CodeOutlined),
    path: "/jabali-panel/php-settings",
  },
  {
    key: "cron",
    label: "Cron",
    icon: navIcon(CalendarCheckOutlined),
    path: "/jabali-panel/cron",
  },
  {
    key: "backups",
    label: "Backup/Restore",
    icon: navIcon(SaveOutlined),
    path: "/jabali-panel/backups",
  },
];

/**
 * Pick the best-matching menu entry for the current pathname using
 * longest-prefix match. Ensures /jabali-admin/users/create still
 * highlights "Users".
 */
export function selectedNavKey(
  items: NavItem[],
  pathname: string,
): string | undefined {
  // matchPatterns win over startsWith — they're explicit overrides for
  // nested routes that semantically belong to a different sidebar entry
  // than the longest path prefix would pick.
  const byPattern = items.find((item) =>
    item.matchPatterns?.some((re) => re.test(pathname)),
  );
  if (byPattern) return byPattern.key;
  return [...items]
    .sort((a, b) => b.path.length - a.path.length)
    .find((item) => pathname.startsWith(item.path))?.key;
}
