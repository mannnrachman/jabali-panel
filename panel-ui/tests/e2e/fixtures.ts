// Shared Playwright fixtures — one place to mock the panel's API surface
// so tests focus on UI behaviour, not transport wiring.
//
// `mockApi({ me, users })` installs route handlers for every endpoint
// the SPA calls in the happy path. Tests override individual routes
// (e.g. to simulate a 409 on create) by calling page.route() AFTER
// mockApi returns.
import type { Page, Route } from "@playwright/test";
import { test as base, expect } from "@playwright/test";

// ---------- type shape of the mock world ----------

export type MockUser = {
  id: string;
  email: string;
  name_first?: string;
  name_last?: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
};

export type MockPackage = {
  id: string;
  name: string;
  disk_quota_mb: number;
  bandwidth_quota_mb: number;
  max_domains: number;
  max_email_accounts: number;
  max_databases: number;
  max_ftp_accounts: number;
  ssh_enabled: boolean;
  cgi_enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type MockDomain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  nginx_custom_directives: string;
  created_at: string;
  updated_at: string;
};

export type MockState = {
  // /me response (identity). null → treat as unauthenticated.
  me: MockUser | null;
  // users list returned by GET /users (the list endpoint); default []
  users?: MockUser[];
  packages?: MockPackage[];
  domains?: MockDomain[];
};

// ---------- core installer ----------

export async function mockApi(page: Page, initial: MockState): Promise<void> {
  // Mutable state. The SPA starts every test logged OUT — `session`
  // becomes the active identity only after a successful /auth/login.
  // `initial.me` is the persona the test wants to sign in AS; it's
  // matched against the login form's email to decide whether the login
  // call succeeds.
  const state: {
    expected: MockUser | null; // identity that login will accept
    session: MockUser | null; // currently signed-in user (null = logged out)
    users: MockUser[];
    packages: MockPackage[];
    domains: MockDomain[];
    accessToken: string | null;
  } = {
    expected: initial.me,
    session: null,
    users: initial.users ?? (initial.me ? [initial.me] : []),
    packages: initial.packages ?? [],
    domains: initial.domains ?? [],
    accessToken: null,
  };

  // ---- auth ----
  await page.route("**/api/v1/auth/login", async (route) => {
    const req = route.request();
    const body = req.postDataJSON() as { email: string; password: string };
    if (state.expected && body.email === state.expected.email) {
      state.session = state.expected;
      state.accessToken = "mock-access-token";
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          access_token: state.accessToken,
          token_type: "Bearer",
          expires_in: 900,
          user: state.session,
        }),
      });
    }
    return route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: "invalid_credentials" }),
    });
  });

  await page.route("**/api/v1/auth/logout", async (route) => {
    state.accessToken = null;
    state.session = null;
    return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
  });

  // Refresh requires an active session. This mirrors the real server —
  // the refresh cookie is set at login time and invalidated at logout,
  // so a cold page load (no prior login in this session) must 401.
  await page.route("**/api/v1/auth/refresh", async (route) => {
    if (state.session) {
      state.accessToken = "mock-access-token-refreshed";
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          access_token: state.accessToken,
          expires_in: 900,
        }),
      });
    }
    return route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: "invalid_refresh" }),
    });
  });

  // ---- identity ----
  await page.route("**/api/v1/me", async (route) => {
    if (state.session && state.accessToken) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: state.session.id,
          email: state.session.email,
          is_admin: state.session.is_admin,
        }),
      });
    }
    return route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: "missing_authorization" }),
    });
  });

  // ---- system (dashboard) ----
  await page.route("**/api/v1/system/info", async (route) => {
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        hostname: "test-server",
        uptime_seconds: 86400,
        load_avg: [0.15, 0.10, 0.05],
        cpu_count: 4,
        mem_total_kb: 16384000,
        mem_available_kb: 8192000,
        mem_used_kb: 8192000,
        partitions: [
          {
            mount_point: "/",
            total_bytes: 53687091200,
            used_bytes: 21474836480,
            free_bytes: 32212254720,
          },
        ],
      }),
    });
  });

  await page.route("**/api/v1/system/services", async (route) => {
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        services: [
          { name: "nginx", active: "active" },
          { name: "mariadb", active: "active" },
          { name: "php8.3-fpm", active: "active" },
          { name: "stalwart-mail", active: "inactive" },
        ],
      }),
    });
  });

  // ---- users CRUD ----
  await page.route(/\/api\/v1\/users(\?.*)?$/, async (route) => {
    const req = route.request();
    if (req.method() === "GET") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: state.users,
          total: state.users.length,
          page: 1,
          page_size: 20,
        }),
      });
    }
    if (req.method() === "POST") {
      const body = req.postDataJSON() as Partial<MockUser> & {
        password: string;
      };
      const now = new Date().toISOString();
      const newUser: MockUser = {
        id: `01${Math.random().toString(36).slice(2, 10).toUpperCase().padEnd(24, "0")}`,
        email: body.email ?? "",
        name_first: body.name_first ?? "",
        name_last: body.name_last ?? "",
        is_admin: body.is_admin ?? false,
        created_at: now,
        updated_at: now,
      };
      state.users.push(newUser);
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(newUser),
      });
    }
    return unsupported(route);
  });

  await page.route(/\/api\/v1\/users\/[^/?]+(\?.*)?$/, async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const id = decodeURIComponent(url.pathname.split("/").pop() ?? "");
    const idx = state.users.findIndex((u) => u.id === id);

    if (req.method() === "GET") {
      if (idx < 0) return notFound(route);
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(state.users[idx]),
      });
    }
    if (req.method() === "PATCH") {
      if (idx < 0) return notFound(route);
      const patch = req.postDataJSON() as Partial<MockUser>;
      state.users[idx] = {
        ...state.users[idx],
        ...patch,
        updated_at: new Date().toISOString(),
      };
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(state.users[idx]),
      });
    }
    if (req.method() === "DELETE") {
      if (idx < 0) return notFound(route);
      state.users.splice(idx, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return unsupported(route);
  });

  // ---- packages CRUD ----
  await page.route(/\/api\/v1\/packages(\?.*)?$/, async (route) => {
    const req = route.request();
    if (req.method() === "GET") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: state.packages,
          total: state.packages.length,
          page: 1,
          page_size: 20,
        }),
      });
    }
    if (req.method() === "POST") {
      const body = req.postDataJSON() as Partial<MockPackage>;
      const now = new Date().toISOString();
      const newPackage: MockPackage = {
        id: `01${Math.random().toString(36).slice(2, 10).toUpperCase().padEnd(24, "0")}`,
        name: body.name ?? "",
        disk_quota_mb: body.disk_quota_mb ?? 0,
        bandwidth_quota_mb: body.bandwidth_quota_mb ?? 0,
        max_domains: body.max_domains ?? 0,
        max_email_accounts: body.max_email_accounts ?? 0,
        max_databases: body.max_databases ?? 0,
        max_ftp_accounts: body.max_ftp_accounts ?? 0,
        ssh_enabled: body.ssh_enabled ?? false,
        cgi_enabled: body.cgi_enabled ?? false,
        created_at: now,
        updated_at: now,
      };
      state.packages.push(newPackage);
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(newPackage),
      });
    }
    return unsupported(route);
  });

  await page.route(/\/api\/v1\/packages\/[^/?]+(\?.*)?$/, async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const id = decodeURIComponent(url.pathname.split("/").pop() ?? "");
    const idx = state.packages.findIndex((p) => p.id === id);

    if (req.method() === "GET") {
      if (idx < 0) return notFound(route);
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(state.packages[idx]),
      });
    }
    if (req.method() === "PATCH") {
      if (idx < 0) return notFound(route);
      const patch = req.postDataJSON() as Partial<MockPackage>;
      state.packages[idx] = {
        ...state.packages[idx],
        ...patch,
        updated_at: new Date().toISOString(),
      };
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(state.packages[idx]),
      });
    }
    if (req.method() === "DELETE") {
      if (idx < 0) return notFound(route);
      state.packages.splice(idx, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return unsupported(route);
  });

  // ---- domains CRUD ----
  await page.route(/\/api\/v1\/domains(\?.*)?$/, async (route) => {
    const req = route.request();
    if (req.method() === "GET") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          data: state.domains,
          total: state.domains.length,
          page: 1,
          page_size: 20,
        }),
      });
    }
    if (req.method() === "POST") {
      const body = req.postDataJSON() as Partial<MockDomain>;
      const now = new Date().toISOString();
      const newDomain: MockDomain = {
        id: `01${Math.random().toString(36).slice(2, 10).toUpperCase().padEnd(24, "0")}`,
        user_id: body.user_id ?? state.session?.id ?? "",
        name: body.name ?? "",
        doc_root: body.doc_root ?? "/var/www/" + (body.name ?? "example").replace(/\./g, "_"),
        is_enabled: body.is_enabled ?? true,
        nginx_custom_directives: body.nginx_custom_directives ?? "",
        created_at: now,
        updated_at: now,
      };
      state.domains.push(newDomain);
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(newDomain),
      });
    }
    return unsupported(route);
  });

  await page.route(/\/api\/v1\/domains\/[^/?]+(\?.*)?$/, async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const id = decodeURIComponent(url.pathname.split("/").pop() ?? "");
    const idx = state.domains.findIndex((d) => d.id === id);

    if (req.method() === "GET") {
      if (idx < 0) return notFound(route);
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(state.domains[idx]),
      });
    }
    if (req.method() === "PATCH") {
      if (idx < 0) return notFound(route);
      const patch = req.postDataJSON() as Partial<MockDomain>;
      state.domains[idx] = {
        ...state.domains[idx],
        ...patch,
        updated_at: new Date().toISOString(),
      };
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(state.domains[idx]),
      });
    }
    if (req.method() === "DELETE") {
      if (idx < 0) return notFound(route);
      state.domains.splice(idx, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return unsupported(route);
  });
}

function notFound(route: Route) {
  return route.fulfill({
    status: 404,
    contentType: "application/json",
    body: JSON.stringify({ error: "not_found" }),
  });
}

function unsupported(route: Route) {
  return route.fulfill({
    status: 405,
    contentType: "application/json",
    body: JSON.stringify({ error: "method_not_allowed" }),
  });
}

// ---------- canned personas ----------

export const admin: MockUser = {
  id: "01KPADMIN00000000000000000",
  email: "admin@test.local",
  name_first: "Test",
  name_last: "Admin",
  is_admin: true,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

export const user: MockUser = {
  id: "01KPUSER000000000000000000",
  email: "user@test.local",
  name_first: "Test",
  name_last: "User",
  is_admin: false,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

// ---------- helper: sign in through the login form ----------

export async function signIn(page: Page, u: MockUser, password = "anypassword123"): Promise<void> {
  await page.goto("/login");
  await page.getByLabel(/email/i).fill(u.email);
  await page.getByLabel(/password/i).fill(password);
  await page.getByRole("button", { name: /sign in/i }).click();
}

// Re-export expect so test files import everything from one place.
export { base as test, expect };
