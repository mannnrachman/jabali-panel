// LoginPage render smoke test. Uses a minimal Refine wrapper so useLogin
// has a provider to consult; we don't exercise the full login flow here
// (that's integration territory) — we just confirm the form renders.
import { Refine } from "@refinedev/core";
import { render, screen } from "@testing-library/react";
import { BrowserRouter } from "react-router";
import { describe, expect, it } from "vitest";

import { LoginPage } from "./Login";

const noopAuthProvider = {
  login: async () => ({ success: false }),
  logout: async () => ({ success: true }),
  check: async () => ({ authenticated: false }),
  onError: async () => ({}),
};

describe("LoginPage", () => {
  it("renders the email + password fields and a submit button", () => {
    render(
      <BrowserRouter>
        <Refine authProvider={noopAuthProvider}>
          <LoginPage />
        </Refine>
      </BrowserRouter>,
    );

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
});
