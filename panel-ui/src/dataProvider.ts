// Data provider for Refine. We use @refinedev/simple-rest against our
// /api/v1 root, but inject our own axios instance so every CRUD request
// gets the Authorization header + 401-refresh interceptor.
//
// The panel's list endpoints return { data, total, page, page_size }; the
// simple-rest provider expects { data } + an X-Total-Count header OR the
// total in the body. We'll adapt when wiring the first real resource in
// Phase 9; for now the shape is good enough for /me and the login flow.
import simpleRestProvider from "@refinedev/simple-rest";

import { apiClient } from "./apiClient";

export const dataProvider = simpleRestProvider("/api/v1", apiClient);
