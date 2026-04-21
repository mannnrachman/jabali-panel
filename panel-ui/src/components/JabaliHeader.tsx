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
import { LogoutOutlined, UserOutlined } from "@ant-design/icons";
import {
  AutoComplete,
  Avatar,
  Button,
  Dropdown,
  Layout,
  Space,
  theme,
} from "antd";
import type { MenuProps } from "antd";
import type { BaseSelectRef } from "rc-select";
import { useLocation, useNavigate } from "react-router";

import { apiClient } from "../apiClient";
import { useAuth } from "../auth/AuthContext";
import { JabaliTitle } from "./JabaliTitle";
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

export function JabaliHeader() {
  const { user, logout } = useAuth();
  const { token } = theme.useToken();
  const [query, setQuery] = useState("");
  const [groups, setGroups] = useState<OptionGroup[]>([]);
  const inputRef = useRef<BaseSelectRef | null>(null);
  const location = useLocation();
  const navigate = useNavigate();

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
        inputRef.current?.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  // Debounced remote fetch. 250ms is tight enough to feel live without
  // hammering the API; an empty/whitespace query clears the dropdown.
  useEffect(() => {
    const trimmed = query.trim();
    if (!trimmed) {
      setGroups([]);
      return;
    }
    let cancelled = false;
    const timer = setTimeout(async () => {
      const qs = new URLSearchParams({ q: trimmed, page_size: "5" }).toString();
      const next: OptionGroup[] = [];
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
  }, [query, isAdminShell]);

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
  };

  const handleSelect = (value: string) => {
    const [kind, id] = value.split(":");
    if (!id) return;
    if (kind === "user") {
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
        const inAdminShell = location.pathname.startsWith("/jabali-admin/");
        if (inAdminShell) {
          // Admins don't have a panel-side profile page; Kratos self-service
          // settings covers email/password/2FA uniformly (M20).
          window.location.assign("/.ory/self-service/settings/browser");
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

  return (
    <Header
      style={{
        backgroundColor: token.colorBgElevated,
        height: 64,
        lineHeight: "normal",
        padding: "0 24px",
        display: "flex",
        alignItems: "center",
        gap: 16,
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
      }}
    >
      <div style={{ flexShrink: 0 }}>
        <JabaliTitle />
      </div>

      {/* Middle column: flex:1 lets it absorb the slack between the
          logo and the right-side actions, and justifyContent:center
          keeps the capped-width AutoComplete visually centered even
          on ultra-wide displays. */}
      <div
        style={{
          flex: 1,
          display: "flex",
          justifyContent: "center",
          minWidth: 0,
        }}
      >
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
          placeholder="Search users, domains… (/)"
          allowClear
          style={{ width: "100%", maxWidth: 400 }}
          popupMatchSelectWidth={false}
          filterOption={false}
        />
      </div>

      <Space size={4}>
        <ThemeToggle />
        <Dropdown menu={{ items: userMenu }} placement="bottomRight">
          <Button type="link" icon={<Avatar icon={<UserOutlined />} />}>
            &nbsp;{email || "…"}
          </Button>
        </Dropdown>
      </Space>
    </Header>
  );
}
