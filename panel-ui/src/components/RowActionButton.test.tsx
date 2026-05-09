import { render, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import { RowActionButton } from "./RowActionButton";

// Tiny inline icon — keeps test isolated from @icons shim.
const Icon = () => <span data-testid="icon">★</span>;

describe("RowActionButton", () => {
  it("renders the icon and label", () => {
    const { getByTestId, getByText } = render(
      <RowActionButton icon={<Icon />}>Edit</RowActionButton>,
    );
    expect(getByTestId("icon")).toBeTruthy();
    expect(getByText("Edit")).toBeTruthy();
  });

  it("calls onClick when clicked", () => {
    const fn = vi.fn();
    const { getByRole } = render(
      <RowActionButton icon={<Icon />} onClick={fn}>
        Edit
      </RowActionButton>,
    );
    fireEvent.click(getByRole("button"));
    expect(fn).toHaveBeenCalledOnce();
  });

  it("renders icon-only when children omitted", () => {
    const { getByRole, getByTestId } = render(
      <RowActionButton icon={<Icon />} aria-label="settings" />,
    );
    expect(getByRole("button", { name: /settings/i })).toBeTruthy();
    expect(getByTestId("icon")).toBeTruthy();
  });

  it("respects disabled prop", () => {
    const fn = vi.fn();
    const { getByRole } = render(
      <RowActionButton icon={<Icon />} disabled onClick={fn}>
        Disabled
      </RowActionButton>,
    );
    const btn = getByRole("button");
    expect(btn).toBeDisabled();
    fireEvent.click(btn);
    expect(fn).not.toHaveBeenCalled();
  });

  it("forwards loading prop", () => {
    const { container } = render(
      <RowActionButton icon={<Icon />} loading>
        Loading
      </RowActionButton>,
    );
    // AntD adds .ant-btn-loading to a loading button.
    expect(container.querySelector(".ant-btn-loading")).not.toBeNull();
  });

  it("danger flag flips color to danger", () => {
    // Smoke test — verify danger=true renders without crashing AND
    // the `aria-label` works under danger so RowDeleteButton-equivalent
    // semantics hold.
    const { getByRole } = render(
      <RowActionButton icon={<Icon />} danger aria-label="delete">
        Delete
      </RowActionButton>,
    );
    expect(getByRole("button", { name: /delete/i })).toBeTruthy();
  });
});
