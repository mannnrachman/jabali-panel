import { describe, it, expect } from "vitest";

import { humanBytes } from "./bytes";

describe("humanBytes", () => {
  it("returns '0 B' for zero, null, undefined, and negatives", () => {
    expect(humanBytes(0)).toBe("0 B");
    expect(humanBytes(null)).toBe("0 B");
    expect(humanBytes(undefined)).toBe("0 B");
    expect(humanBytes(-100)).toBe("0 B");
  });

  it("renders bytes as floored integer with B suffix", () => {
    expect(humanBytes(1)).toBe("1 B");
    expect(humanBytes(512)).toBe("512 B");
    expect(humanBytes(1023)).toBe("1023 B");
  });

  it("steps up to KB at 1024 boundary with one decimal", () => {
    expect(humanBytes(1024)).toBe("1.0 KB");
    expect(humanBytes(1536)).toBe("1.5 KB");
  });

  it("steps up through MB / GB / TB / PB", () => {
    expect(humanBytes(1024 * 1024)).toBe("1.0 MB");
    expect(humanBytes(1024 * 1024 * 1024)).toBe("1.0 GB");
    expect(humanBytes(1024 ** 4)).toBe("1.0 TB");
    expect(humanBytes(1024 ** 5)).toBe("1.0 PB");
  });

  it("caps at PB even for petabyte+ values", () => {
    expect(humanBytes(1024 ** 6)).toBe("1024.0 PB");
  });

  it("rounds to one decimal", () => {
    expect(humanBytes(1500)).toBe("1.5 KB");
    expect(humanBytes(1234567)).toBe("1.2 MB");
  });
});
