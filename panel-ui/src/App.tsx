// App root — Refine + AntD + React Router with two fully separated shells.
//
// URL layout:
//   /login                       → LoginPage (shared)
//   /jabali-admin/*              → AdminShell  (admins only, gated)
//   /jabali-panel/*              → UserShell   (non-admins only, gated)
//   /                            → role-based redirect
//
// Why two shells instead of one role-filtered tree:
//   - No runtime risk of an admin menu item rendering for a user (the
//     two shells use distinct, hardcoded sidebars).
//   - URLs themselves are segregated, so browser history, bookmarks,
//     and any future access logs make the two surfaces unambiguous.
//   - Adding an admin page can't accidentally add a user page.
//
// The shells share providers: one Refine component wraps the whole app
// so authProvider and dataProvider state is consistent regardless of
// which shell you're in.
import { Authenticated, Refine } from "@refinedev/core";
import { useNotificationProvider } from "@refinedev/antd";
import routerProvider, {
  CatchAllNavigate,
  DocumentTitleHandler,
  UnsavedChangesNotifier,
} from "@refinedev/react-router";
import { BrowserRouter, Navigate, Outlet, Route, Routes } from "react-router";
import { ConfigProvider } from "antd";

import {
  AppstoreOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  GlobalOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import useMuiTheme from "./muiTheme";
import { ThemeModeProvider, useThemeMode } from "./theme/ThemeModeContext";
import { authProvider } from "./authProvider";
import { dataProvider } from "./dataProvider";
import { AdminLayout } from "./shells/AdminLayout";
import { UserLayout } from "./shells/UserLayout";
import { RoleGate } from "./shells/RoleGate";
import { LandingRedirect } from "./shells/LandingRedirect";
import { Dashboard } from "./shells/admin/Dashboard";
import { UserCreate } from "./shells/admin/users/UserCreate";
import { UserEdit } from "./shells/admin/users/UserEdit";
import { UserList } from "./shells/admin/users/UserList";
import { PackageCreate } from "./shells/admin/packages/PackageCreate";
import { PackageEdit } from "./shells/admin/packages/PackageEdit";
import { PackageList } from "./shells/admin/packages/PackageList";
import { DomainCreate } from "./shells/admin/domains/DomainCreate";
import { DomainEdit } from "./shells/admin/domains/DomainEdit";
import { DomainList } from "./shells/admin/domains/DomainList";
import { ServerSettingsPage } from "./shells/admin/settings/ServerSettingsPage";
import { MyProfile } from "./shells/user/MyProfile";
import { UserDomainList } from "./shells/user/domains/UserDomainList";
import { UserDomainCreate } from "./shells/user/domains/UserDomainCreate";
import { DNSRecordsPage } from "./shells/dns/DNSRecordsPage";
import { DNSZonesOverviewPage } from "./shells/admin/dns/DNSZonesOverviewPage";
import { UserDNSZonesOverviewPage } from "./shells/user/dns/UserDNSZonesOverviewPage";
import { SSLManagerPage } from "./shells/admin/ssl/SSLManagerPage";
import { UserSSLManagerPage } from "./shells/user/ssl/UserSSLManagerPage";
import { LoginPage } from "./pages/Login";

const ThemedApp = () => {
  const { mode } = useThemeMode();
  const muiConfig = useMuiTheme(mode);

  return (
    <BrowserRouter>
      <ConfigProvider {...muiConfig}>
        <Refine
          authProvider={authProvider}
          dataProvider={dataProvider}
          routerProvider={routerProvider}
          notificationProvider={useNotificationProvider}
          resources={[
            // Admin shell
            {
              name: "dashboard",
              list: "/jabali-admin/dashboard",
              meta: { label: "Dashboard", icon: <DashboardOutlined />, shell: "admin" },
            },
            {
              name: "users",
              list: "/jabali-admin/users",
              create: "/jabali-admin/users/create",
              edit: "/jabali-admin/users/edit/:id",
              meta: { label: "Users", icon: <TeamOutlined />, shell: "admin" },
            },
            {
              name: "packages",
              list: "/jabali-admin/packages",
              create: "/jabali-admin/packages/create",
              edit: "/jabali-admin/packages/edit/:id",
              meta: { label: "Packages", icon: <AppstoreOutlined />, shell: "admin" },
            },
            {
              name: "domains",
              list: "/jabali-admin/domains",
              create: "/jabali-admin/domains/create",
              edit: "/jabali-admin/domains/edit/:id",
              meta: { label: "Domains", icon: <GlobalOutlined />, shell: "admin" },
            },
            {
              name: "admin-dns",
              list: "/jabali-admin/dns",
              meta: { label: "DNS", icon: <CloudServerOutlined />, shell: "admin" },
            },
            {
              name: "admin-ssl",
              list: "/jabali-admin/ssl",
              meta: { label: "SSL", icon: <SafetyCertificateOutlined />, shell: "admin" },
            },
            {
              name: "settings",
              list: "/jabali-admin/settings",
              meta: { label: "Server Settings", icon: <SettingOutlined />, shell: "admin" },
            },

            // User shell
            {
              name: "profile",
              list: "/jabali-panel/profile",
              meta: { label: "My Profile", icon: <UserOutlined />, shell: "user" },
            },
            {
              name: "user-domains",
              list: "/jabali-panel/domains",
              create: "/jabali-panel/domains/create",
              meta: { label: "Domains", icon: <GlobalOutlined />, shell: "user" },
            },
            {
              name: "user-dns",
              list: "/jabali-panel/dns",
              meta: { label: "DNS", icon: <CloudServerOutlined />, shell: "user" },
            },
            {
              name: "user-ssl",
              list: "/jabali-panel/ssl",
              meta: { label: "SSL", icon: <SafetyCertificateOutlined />, shell: "user" },
            },
          ]}
          options={{
            warnWhenUnsavedChanges: true,
            syncWithLocation: true,
          }}
        >
          <Routes>
            {/* ---------------- admin shell ---------------- */}
            <Route
              path="/jabali-admin"
              element={
                <Authenticated
                  key="admin-auth"
                  fallback={<CatchAllNavigate to="/login" />}
                >
                  <RoleGate require="admin">
                    <AdminLayout />
                  </RoleGate>
                </Authenticated>
              }
            >
              {/* bare /jabali-admin → dashboard as default landing */}
              <Route index element={<Navigate to="dashboard" replace />} />
              <Route path="dashboard" element={<Dashboard />} />
              <Route path="users">
                <Route index element={<UserList />} />
                <Route path="create" element={<UserCreate />} />
                <Route path="edit/:id" element={<UserEdit />} />
              </Route>
              <Route path="packages">
                <Route index element={<PackageList />} />
                <Route path="create" element={<PackageCreate />} />
                <Route path="edit/:id" element={<PackageEdit />} />
              </Route>
              <Route path="domains">
                <Route index element={<DomainList />} />
                <Route path="create" element={<DomainCreate />} />
                <Route path="edit/:id" element={<DomainEdit />} />
                <Route path=":id/dns" element={<DNSRecordsPage />} />
              </Route>
              <Route path="dns" element={<DNSZonesOverviewPage />} />
              <Route path="ssl" element={<SSLManagerPage />} />
              <Route path="settings" element={<ServerSettingsPage />} />
            </Route>

            {/* ---------------- user shell ----------------- */}
            <Route
              path="/jabali-panel"
              element={
                <Authenticated
                  key="user-auth"
                  fallback={<CatchAllNavigate to="/login" />}
                >
                  <RoleGate require="user">
                    <UserLayout />
                  </RoleGate>
                </Authenticated>
              }
            >
              <Route index element={<MyProfile />} />
              <Route path="profile" element={<MyProfile />} />
              <Route path="domains">
                <Route index element={<UserDomainList />} />
                <Route path="create" element={<UserDomainCreate />} />
                <Route path=":id/dns" element={<DNSRecordsPage />} />
              </Route>
              <Route path="dns" element={<UserDNSZonesOverviewPage />} />
              <Route path="ssl" element={<UserSSLManagerPage />} />
            </Route>

            {/* ---------------- public ---------------- */}
            <Route
              element={
                <Authenticated key="public" fallback={<Outlet />}>
                  <LandingRedirect />
                </Authenticated>
              }
            >
              <Route path="/login" element={<LoginPage />} />
            </Route>

            {/* landing / catch-all */}
            <Route
              path="/"
              element={
                <Authenticated
                  key="landing"
                  fallback={<CatchAllNavigate to="/login" />}
                >
                  <LandingRedirect />
                </Authenticated>
              }
            />

            <Route
              path="*"
              element={
                <Authenticated
                  key="catchall"
                  fallback={<CatchAllNavigate to="/login" />}
                >
                  <LandingRedirect />
                </Authenticated>
              }
            />
          </Routes>

          <UnsavedChangesNotifier />
          <DocumentTitleHandler />
        </Refine>
      </ConfigProvider>
    </BrowserRouter>
  );
};

const App = () => (
  <ThemeModeProvider>
    <ThemedApp />
  </ThemeModeProvider>
);

export default App;
