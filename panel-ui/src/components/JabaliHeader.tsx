// JabaliHeader — replacement for @refinedev/antd's ThemedHeaderV2.
//
// The built-in header doesn't expose a slot for extra actions (or a
// brand area), so we render our own: brand lockup on the left, theme
// toggle + user dropdown on the right. Background tracks
// theme.useToken() so the chrome stays in sync with light/dark mode.
//
// Middle section holds a global search input that deep-links to the
// most-relevant list page (Users for @, Domains for ., else heuristic).
import { useEffect, useRef, useState } from "react";
import { LogoutOutlined, UserOutlined, SearchOutlined } from "@ant-design/icons";
import { Avatar, Button, Dropdown, Input, Layout, theme } from "antd";
import type { MenuProps } from "antd";
import { useLogout } from "@refinedev/core";
import { useLocation, useNavigate } from "react-router";

import { getIdentity } from "../identity";
import { JabaliTitle } from "./JabaliTitle";
import { ThemeToggle } from "./ThemeToggle";

const { Header } = Layout;

interface JabaliHeaderProps {
  /** Wordmark shown next to the brand icon. */
  brand?: string;
}

export function JabaliHeader({ brand = "Jabali Panel" }: JabaliHeaderProps) {
  const { mutate: logout } = useLogout();
  const { token } = theme.useToken();
  const [email, setEmail] = useState<string>("");
  const [searchQuery, setSearchQuery] = useState("");
  const searchInputRef = useRef<any>(null);
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    getIdentity().then((me) => setEmail(me?.email ?? ""));
  }, []);

  // Keyboard shortcut: / focuses the search input (unless already typing).
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Only trigger if not already in the search input.
      if (searchInputRef.current && document.activeElement === searchInputRef.current) {
        return;
      }

      // Check if the user is typing in another input/textarea/contenteditable.
      const target = e.target as HTMLElement;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        (target.hasAttribute && target.hasAttribute("contenteditable"))
      ) {
        return;
      }

      if (e.key === "/") {
        e.preventDefault();
        searchInputRef.current?.focus();
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  const handleSearchSubmit = () => {
    const query = searchQuery.trim();
    if (!query) return; // Empty query is a no-op.

    // Derive destination based on query content and current shell.
    let targetPath: string;
    const isAdminShell = location.pathname.startsWith("/jabali-admin/");

    if (query.includes("@")) {
      // Contains @: route to Users (admin-only; users stay in user shell → domains).
      targetPath = isAdminShell ? "/jabali-admin/users" : "/jabali-panel/domains";
    } else if (query.includes(".")) {
      // Contains .: route to Domains.
      targetPath = isAdminShell ? "/jabali-admin/domains" : "/jabali-panel/domains";
    } else {
      // No @ or .: use shell heuristic.
      targetPath = isAdminShell ? "/jabali-admin/users" : "/jabali-panel/domains";
    }

    // Build the filter URL in Refine's format so SearchableTable picks it up.
    const encodedValue = encodeURIComponent(query);
    const filterUrl = `${targetPath}?filters[0][field]=q&filters[0][operator]=contains&filters[0][value]=${encodedValue}`;

    navigate(filterUrl);
    setSearchQuery("");
    searchInputRef.current?.blur();
  };

  const userMenu: MenuProps["items"] = [
    {
      key: "logout",
      icon: <LogoutOutlined />,
      label: "Sign out",
      onClick: () => logout(),
    },
  ];

  return (
    <Header
      style={{
        background: token.colorBgContainer,
        padding: "0 24px",
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
        // Refine's ThemedLayoutV2 uses position: sticky by default; match
        // that so the header stays pinned when the content scrolls.
        position: "sticky",
        top: 0,
        zIndex: 1,
      }}
    >
      <JabaliTitle text={brand} />

      {/* Global search in the middle. */}
      <Input
        ref={searchInputRef}
        placeholder="Search users, domains… (/)"
        prefix={<SearchOutlined style={{ color: token.colorTextTertiary }} />}
        value={searchQuery}
        onChange={(e) => setSearchQuery(e.target.value)}
        onPressEnter={handleSearchSubmit}
        style={{
          flex: 1,
          maxWidth: 480,
          borderRadius: token.borderRadiusLG,
        }}
      />

      <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
        <ThemeToggle />
        <Dropdown menu={{ items: userMenu }} placement="bottomRight">
          <Button type="text" icon={<Avatar size="small" icon={<UserOutlined />} />}>
            &nbsp;{email || "…"}
          </Button>
        </Dropdown>
      </div>
    </Header>
  );
}
