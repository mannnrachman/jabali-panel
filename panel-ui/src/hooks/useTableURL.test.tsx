// useTableURL.test.tsx — covers the URL<->state round-trip.
//
// Why these four cases specifically: they are the reasons a URL-backed
// table behaves differently from component-local state. Hitting any of
// them would regress a refresh, share-link, or back-button flow.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router";
import { describe, expect, it, vi } from "vitest";

import { useTableURL } from "./useTableURL";

vi.mock("../apiClient", () => ({
  apiClient: {
    get: vi.fn().mockResolvedValue({ data: { items: [], total: 0 } }),
  },
}));

function wrap(children: ReactNode, initialEntries: string[] = ["/"]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/" element={children} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

type Capture = {
  params?: ReturnType<typeof useTableURL>["params"];
  setParams?: ReturnType<typeof useTableURL>["setParams"];
  navigate?: ReturnType<typeof useNavigate>;
};

function Probe({ capture }: { capture: Capture }) {
  const r = useTableURL<{ id: string }>({
    resource: "users",
    defaultSort: "email",
  });
  capture.params = r.params;
  capture.setParams = r.setParams;
  capture.navigate = useNavigate();
  return <div data-testid="probe">{r.params.page}</div>;
}

describe("useTableURL", () => {
  it("falls back to defaults when no search params are present", () => {
    const capture: Capture = {};
    wrap(<Probe capture={capture} />);
    expect(capture.params).toEqual({
      page: 1,
      pageSize: 20,
      q: "",
      sort: "email",
      order: "desc",
    });
  });

  it("reads values out of the URL", () => {
    const capture: Capture = {};
    wrap(<Probe capture={capture} />, [
      "/?page=3&pageSize=50&q=alice&sort=created_at&order=asc",
    ]);
    expect(capture.params).toEqual({
      page: 3,
      pageSize: 50,
      q: "alice",
      sort: "created_at",
      order: "asc",
    });
  });

  it("setParams writes the URL; empty values delete the key", async () => {
    const capture: Capture = {};
    wrap(<Probe capture={capture} />, ["/?q=bob"]);
    expect(capture.params?.q).toBe("bob");

    await act(async () => {
      capture.setParams?.({ q: "", page: 5 });
    });

    expect(capture.params).toMatchObject({ q: "", page: 5 });
    expect(screen.getByTestId("probe").textContent).toBe("5");
  });

  it("back navigation restores prior state", async () => {
    const capture: Capture = {};
    wrap(<Probe capture={capture} />, ["/?page=1", "/?page=2"]);
    // MemoryRouter initialEntries with index unset lands on the last entry.
    expect(capture.params?.page).toBe(2);

    await act(async () => {
      capture.navigate?.(-1);
    });

    expect(capture.params?.page).toBe(1);
  });
});
