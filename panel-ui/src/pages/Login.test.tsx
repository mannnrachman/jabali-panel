// LoginPage render + 2FA-stage-transition tests.
//
// The render smoke test was the previous file's only coverage. The
// additional tests exercise the two-stage state machine in Login.tsx:
// when /auth/login returns {twofa_pending: true, twofa_pending_token},
// the component must swap the password form for the challenge form
// without navigating or calling authProvider.login.
import { Refine } from "@refinedev/core";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { BrowserRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../apiClient";
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

describe("LoginPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the email + password fields and a submit button", () => {
    renderLogin();

    // AntD renders <input id="email"> via Form.Item name="email"; RTL's
    // byRole lookup on "textbox" + name uses the <label for=...> linkage.
    expect(
      screen.getByRole("textbox", { name: /email/i }),
    ).toBeInTheDocument();
    // Password input has no role=textbox (type=password → no role), but
    // it's the only Input.Password in the form, so by placeholder / label.
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /sign in/i }),
    ).toBeInTheDocument();
  });

  it("swaps to the 2FA challenge form when login returns twofa_pending", async () => {
    const postSpy = vi.spyOn(apiClient, "post").mockResolvedValue({
      data: {
        twofa_pending: true,
        twofa_pending_token: "pending-jwt",
      },
    } as never);

    renderLogin();

    fireEvent.change(screen.getByRole("textbox", { name: /email/i }), {
      target: { value: "alice@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => {
      expect(postSpy).toHaveBeenCalledWith("/auth/login", {
        email: "alice@example.com",
        password: "hunter2",
      });
    });

    // Challenge form now visible; password form gone.
    expect(
      await screen.findByRole("button", { name: /^verify$/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText(/6-digit code/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /sign in/i }),
    ).not.toBeInTheDocument();
  });

  it("shows backup-code input after clicking 'Use backup code'", async () => {
    vi.spyOn(apiClient, "post").mockResolvedValue({
      data: {
        twofa_pending: true,
        twofa_pending_token: "pending-jwt",
      },
    } as never);

    renderLogin();

    fireEvent.change(screen.getByRole("textbox", { name: /email/i }), {
      target: { value: "alice@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    // Wait for challenge form.
    await screen.findByRole("button", { name: /^verify$/i });

    fireEvent.click(screen.getByRole("button", { name: /use backup code/i }));

    // Backup-code input has its own label.
    expect(
      await screen.findByLabelText(/backup code/i),
    ).toBeInTheDocument();
    // And the "swap back" link is now visible.
    expect(
      screen.getByRole("button", { name: /use authenticator/i }),
    ).toBeInTheDocument();
  });

  it("shows invalid_credentials error and stays on password stage", async () => {
    vi.spyOn(apiClient, "post").mockRejectedValue({
      response: { data: { error: "invalid_credentials" } },
    });

    renderLogin();

    fireEvent.change(screen.getByRole("textbox", { name: /email/i }), {
      target: { value: "alice@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "wrong" },
    });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    expect(
      await screen.findByText(/incorrect email or password/i),
    ).toBeInTheDocument();
    // Still on password stage.
    expect(
      screen.getByRole("button", { name: /sign in/i }),
    ).toBeInTheDocument();
  });
});
