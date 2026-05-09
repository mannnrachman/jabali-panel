import { render } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { Sparkline } from "./Sparkline";

describe("Sparkline", () => {
  it("renders an empty rect when given no data", () => {
    const { container } = render(<Sparkline data={[]} width={100} height={20} />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("width")).toBe("100");
    expect(svg?.getAttribute("height")).toBe("20");
    // Empty path → placeholder rect, not a <path>
    expect(container.querySelector("rect")).not.toBeNull();
    expect(container.querySelector("path")).toBeNull();
  });

  it("renders a path for non-empty data", () => {
    const { container } = render(
      <Sparkline
        data={[
          { x: "2026-05-01", y: 100 },
          { x: "2026-05-02", y: 200 },
          { x: "2026-05-03", y: 150 },
        ]}
      />,
    );
    const paths = container.querySelectorAll("path");
    // filled=true (default) → fill path + stroke path
    expect(paths.length).toBe(2);
    // First path = fill (has Z to close)
    expect(paths[0]?.getAttribute("d")).toMatch(/Z$/);
    // Second path = stroke (M ... L ...)
    expect(paths[1]?.getAttribute("d")).toMatch(/^M /);
  });

  it("omits the fill path when filled=false", () => {
    const { container } = render(
      <Sparkline
        data={[
          { x: "a", y: 1 },
          { x: "b", y: 2 },
        ]}
        filled={false}
      />,
    );
    expect(container.querySelectorAll("path")).toHaveLength(1);
  });

  it("renders a single-point series without crashing", () => {
    const { container } = render(
      <Sparkline data={[{ x: "today", y: 42 }]} />,
    );
    expect(container.querySelector("svg")).not.toBeNull();
  });

  it("provides an accessible aria-label that calls formatY for max", () => {
    // The component clamps min to 0 (Math.min(...values, 0)) so the
    // aria-label's `min` value is always 0 unless data contains
    // negatives. We assert on max + the point count, both of which
    // come from data.
    const { container } = render(
      <Sparkline
        data={[
          { x: "a", y: 1024 },
          { x: "b", y: 512 },
        ]}
        formatY={(n) => `${n} bytes`}
      />,
    );
    const svg = container.querySelector("svg");
    const label = svg?.getAttribute("aria-label") ?? "";
    expect(label).toContain("1024 bytes"); // max
    expect(label).toContain("2 points");
  });

  it("provides a generic aria-label when formatY is omitted", () => {
    const { container } = render(
      <Sparkline data={[{ x: "a", y: 1 }]} />,
    );
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("aria-label")).toBe("Sparkline of 1 points");
  });
});
