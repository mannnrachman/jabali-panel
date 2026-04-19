// LoginPage render + Kratos flow progression tests.
//
// The page fetches a Kratos login flow on mount, renders the flow's
// ui.nodes as AntD form fields, and submits to flow.ui.action. This
// test suite stubs the kratos wrapper so we can exercise the render
// path and the AAL1→AAL2 transition without a live Kratos instance.
import { Refine } from "@refinedev/core";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { BrowserRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as kratos from "../kratos";
import { LoginPage } from "./Login";

const noopAuthProvider = {
  login: async () => ({ success: false }),
  logout: async () => ({ success: true }),
  check: async () => ({ authenticated: false }),
  onError: async () => ({}),
};

function renderLogin() {
  return render(
    <BrowserRouter>
      <Refine authProvider={noopAuthProvider}>
        <LoginPage />
      </Refine>
    </BrowserRouter>,
  );
}

function passwordFlow(): kratos.KratosFlow {
  return {
    id: "flow-aal1",
    type: "browser",
    expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
    issued_at: new Date().toISOString(),
    request_url: "http://localhost/.ory/self-service/login/browser",
    ui: {
      action: "http://localhost/.ory/self-service/login?flow=flow-aal1",
      method: "POST",
      nodes: [
        {
          type: "input",
          group: "default",
          attributes: {
            name: "csrf_token",
            type: "hidden",
            value: "csrf-abc",
            required: true,
          },
        },
        {
          type: "input",
          group: "password",
          attributes: {
            name: "identifier",
            type: "text",
            required: true,
            autocomplete: "email",
          },
          meta: { label: { text: "Email" } },
        },
        {
          type: "input",
          group: "password",
          attributes: {
            name: "password",
            type: "password",
            required: true,
            autocomplete: "current-password",
          },
          meta: { label: { text: "Password" } },
        },
      ],
    },
    requested_aal: "aal1",
  };
}

function totpFlow(): kratos.KratosFlow {
  return {
    id: "flow-aal2",
    type: "browser",
    expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
    issued_at: new Date().toISOString(),
    request_url: "http://localhost/.ory/self-service/login/browser",
    ui: {
      action: "http://localhost/.ory/self-service/login?flow=flow-aal2",
      method: "POST",
      nodes: [
        {
          type: "input",
          group: "default",
          attributes: {
            name: "csrf_token",
            type: "hidden",
            value: "csrf-def",
          },
        },
        {
          type: "input",
          group: "totp",
          attributes: {
            name: "totp_code",
            type: "text",
            required: true,
            autocomplete: "one-time-code",
          },
          meta: { label: { text: "Authentication code" } },
        },
      ],
    },
    requested_aal: "aal2",
  };
}

describe("LoginPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the fields from the password-group flow", async () => {
    vi.spyOn(kratos, "initLoginFlow").mockResolvedValue(passwordFlow());

    renderLogin();

    await waitFor(() => {
      expect(screen.getByLabelText(/email/i)).toBeInTheDocument();
    });
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /sign in/i }),
    ).toBeInTheDocument();
  });

  it("shows an error when flow initialisation fails", async () => {
    vi.spyOn(kratos, "initLoginFlow").mockRejectedValue(new Error("boom"));

    renderLogin();

    await waitFor(() => {
      expect(
        screen.getByText(/could not start the sign-in flow/i),
      ).toBeInTheDocument();
    });
  });

  it("switches to TOTP input when the flow continues to AAL2", async () => {
    vi.spyOn(kratos, "initLoginFlow").mockResolvedValue(passwordFlow());
    vi.spyOn(kratos, "submitLoginFlow").mockResolvedValue({
      kind: "continue",
      flow: totpFlow(),
    });

    renderLogin();

    await waitFor(() =>
      expect(screen.getByLabelText(/email/i)).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "alice@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "pw-very-good" },
    });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => {
      expect(screen.getByLabelText(/authentication code/i)).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: /verify/i })).toBeInTheDocument();
  });

  it("surfaces top-level flow errors into an alert", async () => {
    const flow = passwordFlow();
    flow.ui.messages = [
      {
        id: 4000006,
        text: "The provided credentials are invalid.",
        type: "error",
      },
    ];
    vi.spyOn(kratos, "initLoginFlow").mockResolvedValue(flow);

    renderLogin();

    await waitFor(() => {
      expect(
        screen.getByText(/provided credentials are invalid/i),
      ).toBeInTheDocument();
    });
  });
});
