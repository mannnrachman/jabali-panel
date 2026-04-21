// JabaliHeader — top bar rendered at the top of both shells.
//
// Slots: global search on the left, theme toggle + user dropdown on
// the right. Rendered directly as <Layout.Header>, styled to match
// what Refine's ThemedHeaderV2 used to produce (so we don't spook
// operators who already know the chrome).
import { useEffect, useRef, useState } from "react";
import { LogoutOutlined, UserOutlined } from "@ant-design/icons";
import { Avatar, Button, Dropdown, Input, Layout, Space, theme } from "antd";
import type { InputRef, MenuProps } from "antd";
import { useLocation, useNavigate } from "react-router";

import { useAuth } from "../auth/AuthContext";
import { JabaliTitle } from "./JabaliTitle";
import { ThemeToggle } from "./ThemeToggle";

const { Header } = Layout;

export function JabaliHeader() {
  const { user, logout } = useAuth();
  const { token } = theme.useToken();
  const [searchQuery, setSearchQuery] = useState("");
  const searchInputRef = useRef<InputRef | null>(null);
  const location = useLocation();
  const navigate = useNavigate();

  const email = user?.email ?? "";

  // Keyboard shortcut: "/" focuses the search input unless the user is
  // already typing in a form field. Ignore when focus is inside an
  // input, textarea, or a contenteditable element — otherwise typing
  // a literal slash in a text field would steal focus away.
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (
        searchInputRef.current &&
        document.activeElement === searchInputRef.current.input
      ) {
        return;
      }
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
    if (!query) return;

    const isAdminShell = location.pathname.startsWith("/jabali-admin/");
    let targetPath: string;
    if (query.includes("@")) {
      targetPath = isAdminShell ? "/jabali-admin/users" : "/jabali-panel/domains";
    } else if (query.includes(".")) {
      targetPath = isAdminShell ? "/jabali-admin/domains" : "/jabali-panel/domains";
    } else {
      targetPath = isAdminShell ? "/jabali-admin/users" : "/jabali-panel/domains";
    }

    // New table hooks read `q` straight from the URL. We keep the old
    // `filters[0]` query-string shape alive for pages that still run
    // on Refine's useTable until Wave C finishes.
    const encodedValue = encodeURIComponent(query);
    const filterUrl = `${targetPath}?q=${encodedValue}&filters[0][field]=q&filters[0][operator]=contains&filters[0][value]=${encodedValue}`;
    navigate(filterUrl);
    setSearchQuery("");
    searchInputRef.current?.blur();
  };

  const handleLogout = async () => {
    await logout();
    // Hard-navigate so the whoami refetch that AuthProvider would
    // otherwise race against the React Router push doesn't have a
    // chance to briefly restore a stale session (RequireAdmin then
    // sees user=null mid-render but the URL has already come back
    // to /jabali-admin). A full page load fixes that cleanly: fresh
    // module state, fresh QueryClient, and /me returns 401 because
    // the ory_kratos_session cookie has just been expired.
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

  return (
    <Header
      style={{
        // Match ThemedHeaderV2's old contract so the page doesn't jump
        // on upgrade: elevated background, 64px band, border underline,
        // content-left padding. Not sticky — the header scrolls with
        // the page.
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

      <Input.Search
        ref={searchInputRef}
        placeholder="Search users, domains… (/)"
        value={searchQuery}
        onChange={(e) => setSearchQuery(e.target.value)}
        onSearch={handleSearchSubmit}
        allowClear
        style={{ flex: 1, maxWidth: 520 }}
      />

      <Space style={{ marginLeft: "auto" }} size={4}>
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
