// JabaliHeader — Header slot for Refine's ThemedLayoutV2.
//
// ThemedLayoutV2 inserts this directly as a child of its inner <Layout>
// (right of the sider). We render the <Layout.Header> ourselves, the same
// way Refine's default ThemedHeaderV2 does, so AntD gives us the proper
// header flex slot. The chrome inside: global search on the left, theme
// toggle + user dropdown pushed to the right.
import { useEffect, useRef, useState } from "react";
import { LogoutOutlined, UserOutlined, SearchOutlined } from "@ant-design/icons";
import { Avatar, Button, Dropdown, Input, Layout, Space, theme } from "antd";
import type { MenuProps } from "antd";
import { useLogout } from "@refinedev/core";
import { useLocation, useNavigate } from "react-router";

import { getIdentity } from "../identity";
import { ThemeToggle } from "./ThemeToggle";

const { Header } = Layout;

export function JabaliHeader() {
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
      if (searchInputRef.current && document.activeElement === searchInputRef.current.input) {
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
        // Match ThemedHeaderV2's exact style contract so we slot in
        // without fighting AntD's default dark Header background.
        backgroundColor: token.colorBgElevated,
        height: 64,
        lineHeight: "normal",
        padding: "0 24px",
        display: "flex",
        alignItems: "center",
        gap: 16,
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
        position: "sticky",
        top: 0,
        zIndex: 1,
      }}
    >
      <Input
        ref={searchInputRef}
        placeholder="Search users, domains… (/)"
        prefix={<SearchOutlined style={{ color: token.colorTextTertiary }} />}
        value={searchQuery}
        onChange={(e) => setSearchQuery(e.target.value)}
        onPressEnter={handleSearchSubmit}
        allowClear
        style={{
          flex: 1,
          maxWidth: 520,
          borderRadius: token.borderRadiusLG,
        }}
      />

      <Space style={{ marginLeft: "auto" }} size={4}>
        <ThemeToggle />
        <Dropdown menu={{ items: userMenu }} placement="bottomRight">
          <Button type="text" icon={<Avatar size="small" icon={<UserOutlined />} />}>
            &nbsp;{email || "…"}
          </Button>
        </Dropdown>
      </Space>
    </Header>
  );
}
