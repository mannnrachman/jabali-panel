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
  AppstoreAddOutlined,
  AppstoreOutlined,
  ClockCircleOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  FolderOutlined,
  GlobalOutlined,
  HomeOutlined,
  KeyOutlined,
  MailOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { createElement } from "react";

export type NavItem = {
  key: string;
  label: string;
  icon: ReactNode;
  path: string;
};

export const adminNav: NavItem[] = [
  {
    key: "dashboard",
    label: "Dashboard",
    icon: createElement(HomeOutlined),
    path: "/jabali-admin/dashboard",
  },
  {
    key: "users",
    label: "Users",
    icon: createElement(TeamOutlined),
    path: "/jabali-admin/users",
  },
  {
    key: "packages",
    label: "Packages",
    icon: createElement(AppstoreOutlined),
    path: "/jabali-admin/packages",
  },
  {
    key: "domains",
    label: "Domains",
    icon: createElement(GlobalOutlined),
    path: "/jabali-admin/domains",
  },
  {
    key: "dns",
    label: "DNS",
    icon: createElement(CloudServerOutlined),
    path: "/jabali-admin/dns",
  },
  {
    key: "ssl",
    label: "SSL",
    icon: createElement(SafetyCertificateOutlined),
    path: "/jabali-admin/ssl",
  },
  {
    key: "settings",
    label: "Server Settings",
    icon: createElement(SettingOutlined),
    path: "/jabali-admin/settings",
  },
  {
    key: "php-pools",
    label: "PHP Manager",
    icon: createElement(ThunderboltOutlined),
    path: "/jabali-admin/php-pools",
  },
  {
    key: "applications",
    label: "Applications",
    icon: createElement(AppstoreAddOutlined),
    path: "/jabali-admin/applications",
  },
];

export const userNav: NavItem[] = [
  {
    key: "dashboard",
    label: "Dashboard",
    icon: createElement(HomeOutlined),
    path: "/jabali-panel/dashboard",
  },
  {
    key: "domains",
    label: "Domains",
    icon: createElement(GlobalOutlined),
    path: "/jabali-panel/domains",
  },
  {
    key: "dns",
    label: "DNS",
    icon: createElement(CloudServerOutlined),
    path: "/jabali-panel/dns",
  },
  {
    key: "ssl",
    label: "SSL",
    icon: createElement(SafetyCertificateOutlined),
    path: "/jabali-panel/ssl",
  },
  {
    key: "php-settings",
    label: "PHP Settings",
    icon: createElement(ThunderboltOutlined),
    path: "/jabali-panel/php-settings",
  },
  {
    key: "databases",
    label: "Databases",
    icon: createElement(DatabaseOutlined),
    path: "/jabali-panel/databases",
  },
  {
    key: "files",
    label: "Files",
    icon: createElement(FolderOutlined),
    path: "/jabali-panel/files",
  },
  {
    key: "applications",
    label: "Applications",
    icon: createElement(AppstoreAddOutlined),
    path: "/jabali-panel/applications",
  },
  {
    key: "ssh-keys",
    label: "SSH Keys",
    icon: createElement(KeyOutlined),
    path: "/jabali-panel/ssh-keys",
  },
  {
    key: "cron",
    label: "Cron",
    icon: createElement(ClockCircleOutlined),
    path: "/jabali-panel/cron",
  },
  {
    key: "mailboxes",
    label: "Email",
    icon: createElement(MailOutlined),
    path: "/jabali-panel/mailboxes",
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
