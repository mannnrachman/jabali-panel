// JabaliHeader — top bar rendered at the top of both shells.
//
// Layout: logo on the left, a centered AutoComplete search in the
// middle (capped to a readable width so it doesn't stretch across
// wide monitors), theme toggle + user dropdown on the right.
//
// The AutoComplete fetches matching users (admin shell only) and
// domains from panel-api as the user types, debounced so each
// keystroke doesn't fire a request. Selecting a result jumps
// straight to that record's edit page; pressing Enter without a
// selection falls back to the list-page filter navigation so the
// search box is still useful when the right row isn't in the top-5.
import { useEffect, useMemo, useRef, useState } from "react";
import {
  LogoutOutlined,
  MenuOutlined,
  SearchOutlined,
  UserOutlined,
} from "@icons";
import {
  AutoComplete,
  Avatar,
  Button,
  Dropdown,
  Grid,
  Input,
  Layout,
  Modal,
  Space,
  theme,
} from "antd";
import type { MenuProps } from "antd";
import type { BaseSelectRef } from "@rc-component/select";
import { useLocation, useNavigate } from "react-router";

import { apiClient } from "../apiClient";
import { useAuth } from "../auth/AuthContext";
import { adminNav, userNav } from "../nav";
import { JabaliTitle } from "./JabaliTitle";
import { NotificationBell } from "./NotificationBell";
import { ThemeToggle } from "./ThemeToggle";

const { Header } = Layout;

// Minimal row shapes we need from /users and /domains — the wire
// envelope is `{ data: T[], total, page, page_size }`, we only read
// `.data` here.
type UserRow = {
  id: string;
  email: string;
  name_first?: string;
  name_last?: string;
};
type DomainRow = { id: string; name: string };

// Encoded so we can dispatch to the right edit route from a single
// AutoComplete value field. The option label shown to the user is
// plain text (email or domain name).
type SearchOption = {
  value: string;
  label: string;
};
type OptionGroup = { label: string; options: SearchOption[] };

type JabaliHeaderProps = {
  /** Render the hamburger menu button on the left. Set by the shell when
   * the persistent <Sider> is hidden (mobile drawer mode). */
  showMenuButton?: boolean;
  onMenuClick?: () => void;
};

export function JabaliHeader({ showMenuButton = false, onMenuClick }: JabaliHeaderProps = {}) {
  const { user, logout } = useAuth();
  const { token } = theme.useToken();
  const [query, setQuery] = useState("");
  const [groups, setGroups] = useState<OptionGroup[]>([]);
  const [searchModalOpen, setSearchModalOpen] = useState(false);
  const inputRef = useRef<BaseSelectRef | null>(null);
  const location = useLocation();
  const navigate = useNavigate();
  const screens = Grid.useBreakpoint();
  // `sm` also covers everything wider (md, lg, …) because AntD
  // breakpoints are cumulative. Below sm (i.e. xs) we collapse the
  // inline search into a button that opens a full-width modal, hide
  // the wordmark next to the logo, and drop the email text in the
  // user-dropdown button.
  const isWide = screens.sm !== false;

  const email = user?.email ?? "";
  const isAdminShell = location.pathname.startsWith("/jabali-admin/");

  // Keyboard shortcut: "/" focuses the search unless the user is
  // already typing in another form field.
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        (target?.hasAttribute && target.hasAttribute("contenteditable"))
      ) {
        return;
      }
      if (e.key === "/") {
        e.preventDefault();
        if (isWide) {
          inputRef.current?.focus();
        } else {
          // On xs the AutoComplete lives in a Modal; open it and let
          // Modal's autoFocus+focusTriggerAfterClose handle focus.
          setSearchModalOpen(true);
        }
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isWide]);

  // The "Pages" group is always present so clicking the input (like
  // the AntD docs example for AutoComplete) shows every destination
  // in the current shell. When the user types, we substring-match the
  // label so unrelated pages drop out of the dropdown.
  const pagesGroup = useMemo<OptionGroup>(() => {
    const items = isAdminShell ? adminNav : userNav;
    const trimmed = query.trim().toLowerCase();
    const filtered = trimmed
      ? items.filter((n) => n.label.toLowerCase().includes(trimmed))
      : items;
    return {
      label: "Pages",
      options: filtered.map((n) => ({
        value: `page:${n.path}`,
        label: n.label,
      })),
    };
  }, [isAdminShell, query]);

  // Debounced remote fetch. 250ms is tight enough to feel live without
  // hammering the API; an empty/whitespace query keeps the Pages
  // group visible but skips the user/domain API calls.
  useEffect(() => {
    const trimmed = query.trim();
    if (!trimmed) {
      setGroups([pagesGroup]);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(async () => {
      const qs = new URLSearchParams({ q: trimmed, page_size: "5" }).toString();
      const next: OptionGroup[] = [];
      if (pagesGroup.options.length > 0) next.push(pagesGroup);
      if (isAdminShell) {
        try {
          const { data } = await apiClient.get<{ data?: UserRow[] }>(
            `/users?${qs}`,
          );
          const rows = data.data ?? [];
          if (rows.length > 0) {
            next.push({
              label: "Users",
              options: rows.map((u) => {
                const name = [u.name_first, u.name_last]
                  .filter(Boolean)
                  .join(" ")
                  .trim();
                return {
                  value: `user:${u.id}`,
                  label: name ? `${u.email} — ${name}` : u.email,
                };
              }),
            });
          }
        } catch {
          // Silent: the search box doesn't need to shout if one
          // endpoint hiccups — the other group may still succeed.
        }
      }
      try {
        const { data } = await apiClient.get<{ data?: DomainRow[] }>(
          `/domains?${qs}`,
        );
        const rows = data.data ?? [];
        if (rows.length > 0) {
          next.push({
            label: "Domains",
            options: rows.map((d) => ({
              value: `domain:${d.id}`,
              label: d.name,
            })),
          });
        }
      } catch {
        /* ignore — see above */
      }
      if (!cancelled) setGroups(next);
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [query, isAdminShell, pagesGroup]);

  // Fallback: if the user hits Enter without picking a suggestion
  // (e.g. their target isn't in the top-5 dropdown), push the query
  // into the relevant list page's ?q= filter so the table view
  // takes over.
  const submitQuery = (raw: string) => {
    const q = raw.trim();
    if (!q) return;
    const encoded = encodeURIComponent(q);
    let targetPath: string;
    if (q.includes("@")) {
      targetPath = isAdminShell
        ? "/jabali-admin/users"
        : "/jabali-panel/domains";
    } else {
      targetPath = isAdminShell
        ? "/jabali-admin/domains"
        : "/jabali-panel/domains";
    }
    navigate(`${targetPath}?q=${encoded}`);
    setQuery("");
    setGroups([]);
    inputRef.current?.blur();
    setSearchModalOpen(false);
  };

  const handleSelect = (value: string) => {
    // value is `kind:payload` — split on the first colon so nav paths
    // containing colons (none today, but don't design in a trap) or
    // UUIDs-with-dashes still route correctly.
    const colon = value.indexOf(":");
    if (colon < 0) return;
    const kind = value.slice(0, colon);
    const id = value.slice(colon + 1);
    if (!id) return;
    if (kind === "page") {
      navigate(id);
    } else if (kind === "user") {
      navigate(`/jabali-admin/users/edit/${id}`);
    } else if (kind === "domain") {
      // User shell has no dedicated edit route — fall back to the
      // filtered list view so the user still lands near the record.
      if (isAdminShell) {
        navigate(`/jabali-admin/domains/edit/${id}`);
      } else {
        navigate(`/jabali-panel/domains`);
      }
    }
    setQuery("");
    setGroups([]);
    inputRef.current?.blur();
    setSearchModalOpen(false);
  };

  const handleLogout = async () => {
    await logout();
    // Hard-navigate so the whoami refetch that AuthProvider would
    // otherwise race against the React Router push doesn't have a
    // chance to briefly restore a stale session.
    window.location.assign("/login");
  };

  const userMenu: MenuProps["items"] = [
    {
      key: "profile",
      icon: <UserOutlined />,
      label: "Profile",
      onClick: () => {
        // Use existing isAdminShell variable
        if (isAdminShell) {
          // Admins don't have a panel-side profile page; Kratos self-service
          // settings covers email/password/2FA uniformly (M20).
          // Use window.location to bypass React Router's relative navigation
          // since /jabali-admin has its own nested /settings route.
          window.location.assign("/settings" + window.location.search);
        } else {
          navigate("/jabali-panel/profile");
        }
      },
    },
    { type: "divider" },
    {
      key: "logout",
      icon: <LogoutOutlined />,
      label: "Sign out",
      onClick: handleLogout,
    },
  ];

  // AntD's `options` prop on AutoComplete accepts both flat and
  // grouped shapes; useMemo keeps the reference stable when `groups`
  // hasn't changed so the dropdown doesn't flicker.
  const options = useMemo(() => groups, [groups]);

  // The search input is one element used in two slots: inline in the
  // header on sm+ or inside a Modal on xs. Rendering a single JSX
  // avoids handler drift between the two instances.
  const searchInput = (
    <AutoComplete
      ref={inputRef}
      value={query}
      options={options}
      onChange={setQuery}
      onSelect={handleSelect}
      // Pressing Enter or clicking a suggestion triggers `onSelect`.
      // When the user types and hits Enter without a highlighted
      // option, the outer Input's onPressEnter catches it and we
      // fall back to the filtered-list navigation.
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          // If a dropdown item is highlighted AntD fires onSelect
          // before this handler; once that ran, query is empty
          // and the fallback below is a no-op.
          submitQuery(query);
        }
      }}
      style={{ width: "100%", maxWidth: isWide ? 400 : undefined }}
      popupMatchSelectWidth={false}
      filterOption={false}
      autoFocus={!isWide && searchModalOpen}
    >
      {/* Child <Input> lets us attach a prefix icon; AutoComplete's
          own `prefix` prop doesn't exist, but passing an Input as
          child routes value + onChange through the outer AutoComplete
          automatically. */}
      <Input
        prefix={<SearchOutlined style={{ color: token.colorTextSecondary }} />}
        placeholder="Search users, domains…"
        allowClear
      />
    </AutoComplete>
  );

  return (
    <Header
      style={{
        backgroundColor: token.colorBgElevated,
        height: 64,
        lineHeight: "normal",
        padding: isWide ? "0 24px" : "0 12px",
        display: "flex",
        alignItems: "center",
        gap: isWide ? 16 : 8,
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
      }}
    >
      {showMenuButton && (
        <Button
          type="text"
          size="large"
          icon={<MenuOutlined />}
          onClick={onMenuClick}
          aria-label="Open navigation menu"
          style={{ flexShrink: 0 }}
        />
      )}

      <div style={{ flexShrink: 0 }}>
        <JabaliTitle showWordmark={isWide} />
      </div>

      {/* Middle column: flex:1 lets it absorb the slack between the
          logo and the right-side actions, and justifyContent:center
          keeps the capped-width AutoComplete visually centered even
          on ultra-wide displays. On xs the inline slot collapses to
          a search-icon button; the AutoComplete itself is reparented
          into a full-width Modal below. */}
      <div
        style={{
          flex: 1,
          display: "flex",
          justifyContent: isWide ? "center" : "flex-end",
          minWidth: 0,
        }}
      >
        {isWide ? (
          searchInput
        ) : (
          <Button
            type="text"
            icon={<SearchOutlined />}
            aria-label="Open search"
            onClick={() => setSearchModalOpen(true)}
          />
        )}
      </div>

      {!isWide && (
        <Modal
          open={searchModalOpen}
          onCancel={() => setSearchModalOpen(false)}
          footer={null}
          title="Search"
          width="100%"
          style={{ top: 16 }}
          destroyOnClose={false}
        >
          {searchInput}
        </Modal>
      )}

      <Space size={4}>
        <NotificationBell />
        <ThemeToggle />
        <Dropdown menu={{ items: userMenu }} placement="bottomRight">
          <Button type="text" icon={<Avatar icon={<UserOutlined />} />}>
            {isWide ? <>&nbsp;{email || "…"}</> : null}
          </Button>
        </Dropdown>
      </Space>
    </Header>
  );
}
