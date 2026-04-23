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
  CalendarCheckOutlined,
  CloudServerOutlined,
  CodeOutlined,
  DatabaseOutlined,
  FolderOutlined,
  GlobalOutlined,
  HomeOutlined,
  KeyOutlined,
  MailOutlined,
  PackageOpenOutlined,
  SettingOutlined,
  ShieldCheckOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from "@icons";
import { createElement } from "react";
import type { ComponentType } from "react";

export type NavItem = {
  key: string;
  label: string;
  icon: ReactNode;
  path: string;
};

// All sidebar icons render a shade larger than AntD's default (14px
// inherited from fontSize). 20px reads comfortably without crowding the
// label and keeps the collapsed-sider footprint tidy. Change here and
// every entry picks it up.
const NAV_ICON_SIZE = 20;

const navIcon = (Icon: ComponentType<{ style?: React.CSSProperties }>) =>
  createElement(Icon, { style: { fontSize: NAV_ICON_SIZE } });

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
    key: "packages",
    label: "Packages",
    icon: navIcon(PackageOpenOutlined),
    path: "/jabali-admin/packages",
  },
  {
    key: "domains",
    label: "Domains",
    icon: navIcon(GlobalOutlined),
    path: "/jabali-admin/domains",
  },
  {
    key: "dns",
    label: "DNS",
    icon: navIcon(CloudServerOutlined),
    path: "/jabali-admin/dns",
  },
  {
    key: "ssl",
    label: "SSL Manager",
    icon: navIcon(ShieldCheckOutlined),
    path: "/jabali-admin/ssl",
  },
  {
    key: "settings",
    label: "Server Settings",
    icon: navIcon(SettingOutlined),
    path: "/jabali-admin/settings",
  },
  {
    key: "php-pools",
    label: "PHP Manager",
    icon: navIcon(ThunderboltOutlined),
    path: "/jabali-admin/php-pools",
  },
  {
    key: "applications",
    label: "Applications",
    icon: navIcon(AppstoreOutlined),
    path: "/jabali-admin/applications",
  },
  {
    key: "ips",
    label: "IP Addresses",
    icon: createElement(GlobalOutlined),
    path: "/jabali-admin/ips",
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
    key: "dns",
    label: "DNS",
    icon: navIcon(CloudServerOutlined),
    path: "/jabali-panel/dns",
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
    key: "applications",
    label: "Applications",
    icon: navIcon(AppstoreOutlined),
    path: "/jabali-panel/applications",
  },
  {
    key: "ssh-keys",
    label: "SSH Keys",
    icon: navIcon(KeyOutlined),
    path: "/jabali-panel/ssh-keys",
  },
  {
    key: "cron",
    label: "Cron",
    icon: navIcon(CalendarCheckOutlined),
    path: "/jabali-panel/cron",
  },
  {
    key: "mail",
    label: "Mail",
    icon: navIcon(MailOutlined),
    path: "/jabali-panel/mail/mailboxes",
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
  return [...items]
    .sort((a, b) => b.path.length - a.path.length)
    .find((item) => pathname.startsWith(item.path))?.key;
}
