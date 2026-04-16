// App root — Refine + AntD + React Router.
//
// Routes:
//   /          → Dashboard, behind <Authenticated>
//   /login     → LoginPage, only shown when not authed (redirects home otherwise)
//   everything else → 404
//
// Phase 9+ will register resources ("users", "domains", …) and let
// Refine auto-wire the list/create/edit/show routes via its
// <NavigateToResource> helper.
import { Authenticated, Refine } from "@refinedev/core";
import { RefineThemes, useNotificationProvider } from "@refinedev/antd";
import routerProvider, {
  CatchAllNavigate,
  UnsavedChangesNotifier,
  DocumentTitleHandler,
} from "@refinedev/react-router";
import { BrowserRouter, Outlet, Route, Routes } from "react-router";
import { ConfigProvider, Layout } from "antd";

import { authProvider } from "./authProvider";
import { dataProvider } from "./dataProvider";
import { DashboardPage } from "./pages/Dashboard";
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
          options={{
            warnWhenUnsavedChanges: true,
            // Phase 8 has no resources; list is an intentional empty array
            // so <NavigateToResource> (used later) has a predictable target.
          }}
        >
          <Routes>
            {/* Protected group: everything inside requires authentication.
                CatchAllNavigate sends the unauth'd viewer to /login. */}
            <Route
              element={
                <Authenticated
                  key="protected"
                  fallback={<CatchAllNavigate to="/login" />}
                >
                  <Layout style={{ minHeight: "100vh" }}>
                    <Outlet />
                  </Layout>
                </Authenticated>
              }
            >
              <Route index element={<DashboardPage />} />
            </Route>

            {/* Public group: login page. Already-authed users get bounced home. */}
            <Route
              element={
                <Authenticated key="public" fallback={<Outlet />}>
                  <CatchAllNavigate to="/" />
                </Authenticated>
              }
            >
              <Route path="/login" element={<LoginPage />} />
            </Route>

            <Route path="*" element={<div>404 — not found</div>} />
          </Routes>

          <UnsavedChangesNotifier />
          <DocumentTitleHandler />
        </Refine>
      </ConfigProvider>
    </BrowserRouter>
  );
};

export default App;
