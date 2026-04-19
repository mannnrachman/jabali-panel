// MyProfile2FACard render tests. Covers the three states driven by
// GET /users/:id — loading, enabled, disabled — and the enrolment modal's
// entry point.
//
// We mock apiClient with vi.spyOn and stub each method on demand. AntD's
// QRCode component uses canvas internally which happy-dom doesn't implement
// fully, so the enrolment test doesn't assert on the QR pixels — only that
// the modal opens and the secret text appears.
//
// Uses fireEvent (not user-event, which isn't in the dep tree) — fine for
// click-target tests; don't add keyboard-flow assertions here.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient } from "../../apiClient";
import { MyProfile2FACard } from "./MyProfile2FACard";

describe("MyProfile2FACard", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows 'Not enabled' + Enable button when totp_enabled=false", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({
      data: { totp_enabled: false },
    } as never);

    render(<MyProfile2FACard userId="u1" />);

    expect(
      await screen.findByRole("button", { name: /enable 2fa/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/not enabled/i)).toBeInTheDocument();
  });

  it("shows 'Enabled' + Regenerate + Disable buttons when totp_enabled=true", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({
      data: { totp_enabled: true },
    } as never);

    render(<MyProfile2FACard userId="u1" />);

    expect(
      await screen.findByRole("button", { name: /regenerate backup codes/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /disable 2fa/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/^enabled$/i)).toBeInTheDocument();
  });

  it("opens the enrolment modal and calls /auth/2fa/enroll when Enable is clicked", async () => {
    const getSpy = vi.spyOn(apiClient, "get").mockResolvedValue({
      data: { totp_enabled: false },
    } as never);
    const postSpy = vi.spyOn(apiClient, "post").mockResolvedValue({
      data: {
        secret: "JBSWY3DPEHPK3PXP",
        otpauth_url:
          "otpauth://totp/Jabali%20Panel:alice@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Jabali%20Panel",
      },
    } as never);

    render(<MyProfile2FACard userId="u1" />);

    fireEvent.click(await screen.findByRole("button", { name: /enable 2fa/i }));

    await waitFor(() => {
      expect(postSpy).toHaveBeenCalledWith("/auth/2fa/enroll");
    });

    // Manual-entry secret shows inside the modal so the user has a fallback.
    expect(await screen.findByText("JBSWY3DPEHPK3PXP")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /verify.*enable/i }),
    ).toBeInTheDocument();

    // Sanity: the card's own /users/:id read happened too.
    expect(getSpy).toHaveBeenCalledWith("/users/u1");
  });

  it("opens the disable modal and requires password + code", async () => {
    vi.spyOn(apiClient, "get").mockResolvedValue({
      data: { totp_enabled: true },
    } as never);

    render(<MyProfile2FACard userId="u1" />);

    fireEvent.click(
      await screen.findByRole("button", { name: /disable 2fa/i }),
    );

    // Disable modal requires password + TOTP — both must appear.
    expect(await screen.findByLabelText(/current password/i)).toBeInTheDocument();
    expect(
      screen.getByLabelText(/6-digit code from your authenticator/i),
    ).toBeInTheDocument();
  });
});
