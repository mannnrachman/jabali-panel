// App root — AntD + TanStack Query + React Router with two fully
// separated shells.
//
// URL layout:
//   /login                       → LoginPage (Kratos-driven, public)
//   /consent                     → OAuth consent screen (auth required)
//   /jabali-admin/*              → AdminShell  (admins only, gated)
//   /jabali-panel/*              → UserShell   (authenticated, gated)
//   /                            → role-based redirect
//
// Why two shells instead of one role-filtered tree:
//   - No runtime risk of an admin menu item rendering for a user (the
//     two shells use distinct, hardcoded sidebars).
//   - URLs themselves are segregated, so browser history, bookmarks,
//     and any future access logs make the two surfaces unambiguous.
//   - Adding an admin page can't accidentally add a user page.
//
// Refine is gone as of M21: the tree composes QueryClientProvider +
// AuthProvider + BrowserRouter + ConfigProvider directly. Every
// protected page re-uses the same whoami cache.
import { QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { ConfigProvider, Empty, Spin } from "antd";
import type { ReactNode } from "react";

import { AuthProvider, useAuth } from "./auth/AuthContext";
import { RequireAdmin } from "./auth/RequireAdmin";
import { RequireUser } from "./auth/RequireUser";
import useMuiTheme from "./muiTheme";
import { queryClient } from "./query";
import { AdminLayout } from "./shells/AdminLayout";
import { UserLayout } from "./shells/UserLayout";
import { LandingRedirect } from "./shells/LandingRedirect";
import { ThemeModeProvider, useThemeMode } from "./theme/ThemeModeContext";
import { Dashboard } from "./shells/admin/Dashboard";
import { UserCreate } from "./shells/admin/users/UserCreate";
import { UserEdit } from "./shells/admin/users/UserEdit";
import { UserList } from "./shells/admin/users/UserList";
import { AdminIPCreate } from "./shells/admin/ips/AdminIPCreate";
import { AdminIPEdit } from "./shells/admin/ips/AdminIPEdit";
import { AdminIPList } from "./shells/admin/ips/AdminIPList";
import { NotificationsTabsPage } from "./shells/admin/notifications/NotificationsTabsPage";
import { AdminSecurityPage } from "./shells/admin/security/AdminSecurityPage";
import { PackageCreate } from "./shells/admin/packages/PackageCreate";
import { PackageEdit } from "./shells/admin/packages/PackageEdit";
import { PackageList } from "./shells/admin/packages/PackageList";
import { DomainCreate } from "./shells/admin/domains/DomainCreate";
import { DomainEdit } from "./shells/admin/domains/DomainEdit";
import { DomainList } from "./shells/admin/domains/DomainList";
import { ServerSettingsPage } from "./shells/admin/settings/ServerSettingsPage";
import { MyProfile } from "./shells/user/MyProfile";
import { UserDashboard } from "./shells/user/UserDashboard";
import { FileManagerPage } from "./shells/user/files/FileManagerPage";
import { UserDomainList } from "./shells/user/domains/UserDomainList";
import { UserDomainCreate } from "./shells/user/domains/UserDomainCreate";
import { UserDatabasesPage } from "./shells/user/databases/UserDatabasesPage";
import { UserDatabaseCreate } from "./shells/user/databases/UserDatabaseCreate";
import { UserDatabaseUserCreate } from "./shells/user/database-users/UserDatabaseUserCreate";
import { DNSRecordsPage } from "./shells/dns/DNSRecordsPage";
import { DNSZonesOverviewPage } from "./shells/admin/dns/DNSZonesOverviewPage";
import { UserDNSZonesOverviewPage } from "./shells/user/dns/UserDNSZonesOverviewPage";
import { SSLManagerPage } from "./shells/admin/ssl/SSLManagerPage";
import { UserSSLManagerPage } from "./shells/user/ssl/UserSSLManagerPage";
import { UserPHPSettingsPage } from "./shells/user/php-settings/UserPHPSettingsPage";
import { UserApplicationList } from "./shells/user/applications/UserApplicationList";
import { UserSSHKeysPage } from "./shells/user/ssh-keys/UserSSHKeysPage";
import { UserCronList } from "./shells/user/cron/UserCronList";
import { MailTabsPage } from "./shells/user/mail/MailTabsPage";
import { AdminApplicationList } from "./shells/admin/applications/AdminApplicationList";
import { PHPVersionsPage } from "./shells/admin/php/PHPVersionsPage";
import { PHPPoolEdit } from "./shells/admin/php-pools/PHPPoolEdit";
import { LoginPage } from "./pages/Login";

// If a logged-in user hits /login, bounce them to their shell home
// instead of letting them see the form. Public routes use this — the
// Kratos-driven LoginPage itself doesn't know about AuthContext, so
// the gate lives here.
function PublicOnly({ children }: { children: ReactNode }) {
  const { user, isLoading } = useAuth();
  if (isLoading) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight: "100vh",
        }}
      >
        <Spin size="large" />
      </div>
    );
  }
  if (user) {
    return (
      <Navigate to={user.isAdmin ? "/jabali-admin" : "/jabali-panel"} replace />
    );
  }
  return <>{children}</>;
}

const ThemedApp = () => {
  const { mode } = useThemeMode();
  const muiConfig = useMuiTheme(mode);

  return (
    <BrowserRouter>
      <ConfigProvider
        {...muiConfig}
        renderEmpty={() => <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />}
      >
        <Routes>
          {/* ---------------- admin shell ---------------- */}
          <Route
            path="/jabali-admin"
            element={
              <RequireAdmin>
                <AdminLayout />
              </RequireAdmin>
            }
          >
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
            <Route path="php-pools">
              <Route index element={<PHPVersionsPage />} />
              <Route path="edit/:id" element={<PHPPoolEdit />} />
            </Route>
            <Route path="applications" element={<AdminApplicationList />} />
            <Route path="ips">
              <Route index element={<AdminIPList />} />
              <Route path="create" element={<AdminIPCreate />} />
              <Route path="edit/:id" element={<AdminIPEdit />} />
            </Route>
            <Route path="security" element={<AdminSecurityPage />} />
            <Route path="notifications">
              <Route index element={<Navigate to="channels" replace />} />
              <Route path=":tab" element={<NotificationsTabsPage />} />
            </Route>
          </Route>

          {/* ---------------- user shell ----------------- */}
          <Route
            path="/jabali-panel"
            element={
              <RequireUser>
                <UserLayout />
              </RequireUser>
            }
          >
            <Route index element={<Navigate to="dashboard" replace />} />
            <Route path="dashboard" element={<UserDashboard />} />
            {/* Profile stays reachable via the header dropdown — no
                longer a sidebar entry as of the dashboard addition. */}
            <Route path="profile" element={<MyProfile />} />
            <Route path="domains">
              <Route index element={<UserDomainList />} />
              <Route path="create" element={<UserDomainCreate />} />
              <Route path=":id/dns" element={<DNSRecordsPage />} />
            </Route>
            <Route path="databases">
              <Route index element={<UserDatabasesPage />} />
              <Route path="create" element={<UserDatabaseCreate />} />
            </Route>
            <Route path="database-users">
              <Route path="create" element={<UserDatabaseUserCreate />} />
            </Route>
            <Route path="dns" element={<UserDNSZonesOverviewPage />} />
            <Route path="ssl" element={<UserSSLManagerPage />} />
            <Route path="php-settings" element={<UserPHPSettingsPage />} />
            <Route path="files" element={<FileManagerPage />} />
            <Route path="applications" element={<UserApplicationList />} />
            <Route path="ssh-keys" element={<UserSSHKeysPage />} />
            <Route path="cron" element={<UserCronList />} />
            <Route path="mail" element={<Navigate to="/jabali-panel/mail/mailboxes" replace />} />
            <Route path="mail/:tab" element={<MailTabsPage />} />
            <Route path="mailboxes" element={<Navigate to="/jabali-panel/mail/mailboxes" replace />} />
          </Route>

          {/* ---------------- public ---------------- */}
          <Route
            path="/login"
            element={
              <PublicOnly>
                <LoginPage />
              </PublicOnly>
            }
          />

          {/* landing / catch-all */}
          <Route path="/" element={<LandingRedirect />} />
          <Route path="*" element={<LandingRedirect />} />
        </Routes>
      </ConfigProvider>
    </BrowserRouter>
  );
};

const App = () => (
  <QueryClientProvider client={queryClient}>
    <AuthProvider>
      <ThemeModeProvider>
        <ThemedApp />
      </ThemeModeProvider>
    </AuthProvider>
  </QueryClientProvider>
);

export default App;
