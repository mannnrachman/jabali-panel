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
import { RefineThemes, useNotificationProvider } from "@refinedev/antd";
import routerProvider, {
  CatchAllNavigate,
  DocumentTitleHandler,
  UnsavedChangesNotifier,
} from "@refinedev/react-router";
import { BrowserRouter, Navigate, Outlet, Route, Routes } from "react-router";
import { ConfigProvider } from "antd";

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
import { MyProfile } from "./shells/user/MyProfile";
import { LoginPage } from "./pages/Login";

const App = () => {
  return (
    <BrowserRouter>
      <ConfigProvider theme={RefineThemes.Blue}>
        <Refine
          authProvider={authProvider}
          dataProvider={dataProvider}
          routerProvider={routerProvider}
          notificationProvider={useNotificationProvider}
          // Resource metadata is still useful for Refine's data hooks
          // (invalidation after mutation, etc) even though the sidebars
          // are hardcoded per shell.
          resources={[
            {
              name: "users",
              list: "/jabali-admin/users",
              create: "/jabali-admin/users/create",
              edit: "/jabali-admin/users/edit/:id",
            },
            {
              name: "packages",
              list: "/jabali-admin/packages",
              create: "/jabali-admin/packages/create",
              edit: "/jabali-admin/packages/edit/:id",
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

export default App;
