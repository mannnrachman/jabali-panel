// App root — Refine + AntD + React Router.
//
// Layout:
//   Protected routes sit under <ThemedLayoutV2>, which provides the
//   sidebar + header. Refine reads the `resources` prop to auto-populate
//   the sidebar with links to the register'd resources.
//
// Routes:
//   /login              → LoginPage (public)
//   /                   → redirects to /users (first resource)
//   /users              → UserList
//   /users/create       → UserCreate
//   /users/edit/:id     → UserEdit
//   everything else     → 404 via AntD's ErrorComponent
import { Authenticated, Refine } from "@refinedev/core";
import {
  ErrorComponent,
  RefineThemes,
  ThemedLayoutV2,
  ThemedTitleV2,
  useNotificationProvider,
} from "@refinedev/antd";
import routerProvider, {
  CatchAllNavigate,
  DocumentTitleHandler,
  NavigateToResource,
  UnsavedChangesNotifier,
} from "@refinedev/react-router";
import { BrowserRouter, Outlet, Route, Routes } from "react-router";
import { ConfigProvider } from "antd";
import { TeamOutlined } from "@ant-design/icons";

import { authProvider } from "./authProvider";
import { dataProvider } from "./dataProvider";
import { LoginPage } from "./pages/Login";
import { UserCreate } from "./pages/users/UserCreate";
import { UserEdit } from "./pages/users/UserEdit";
import { UserList } from "./pages/users/UserList";

const App = () => {
  return (
    <BrowserRouter>
      <ConfigProvider theme={RefineThemes.Blue}>
        <Refine
          authProvider={authProvider}
          dataProvider={dataProvider}
          routerProvider={routerProvider}
          notificationProvider={useNotificationProvider}
          resources={[
            {
              name: "users",
              list: "/users",
              create: "/users/create",
              edit: "/users/edit/:id",
              meta: { label: "Users", icon: <TeamOutlined /> },
            },
          ]}
          options={{
            warnWhenUnsavedChanges: true,
            // Route UX polish: reflect list filters / sorts in the URL
            // so a back-button click brings you back to the same view.
            syncWithLocation: true,
          }}
        >
          <Routes>
            {/* Protected group: wrapped in the full admin layout. */}
            <Route
              element={
                <Authenticated
                  key="protected"
                  fallback={<CatchAllNavigate to="/login" />}
                >
                  <ThemedLayoutV2
                    Title={({ collapsed }) => (
                      <ThemedTitleV2 collapsed={collapsed} text="Jabali Panel" />
                    )}
                  >
                    <Outlet />
                  </ThemedLayoutV2>
                </Authenticated>
              }
            >
              <Route index element={<NavigateToResource resource="users" />} />

              <Route path="/users">
                <Route index element={<UserList />} />
                <Route path="create" element={<UserCreate />} />
                <Route path="edit/:id" element={<UserEdit />} />
              </Route>

              <Route path="*" element={<ErrorComponent />} />
            </Route>

            {/* Public group: login only. Already-authed → redirect home. */}
            <Route
              element={
                <Authenticated key="public" fallback={<Outlet />}>
                  <NavigateToResource resource="users" />
                </Authenticated>
              }
            >
              <Route path="/login" element={<LoginPage />} />
            </Route>
          </Routes>

          <UnsavedChangesNotifier />
          <DocumentTitleHandler />
        </Refine>
      </ConfigProvider>
    </BrowserRouter>
  );
};

export default App;
