import { describe, expect, it } from "vitest";

import { generatePassword } from "./passwordGenerator";

describe("generatePassword", () => {
  it("returns a password of the requested length", () => {
    expect(generatePassword(16)).toHaveLength(16);
    expect(generatePassword(24)).toHaveLength(24);
    expect(generatePassword(4)).toHaveLength(4);
  });

  it("rejects lengths shorter than the guaranteed-class minimum", () => {
    expect(() => generatePassword(3)).toThrow(/length must be >= 4/);
    expect(() => generatePassword(0)).toThrow();
  });

  it("contains at least one lowercase, uppercase, digit, and symbol", () => {
    // Run many iterations — one guaranteed-class seeding could still
    // fail if the shuffle somehow dropped it. Doesn't, but prove it.
    for (let i = 0; i < 500; i++) {
      const pw = generatePassword(12);
      expect(pw).toMatch(/[a-z]/);
      expect(pw).toMatch(/[A-Z]/);
      expect(pw).toMatch(/[0-9]/);
      expect(pw).toMatch(/[!@#%^&*_=+?-]/);
    }
  });

  it("never emits visually ambiguous characters (0, 1, O, I, l)", () => {
    for (let i = 0; i < 500; i++) {
      const pw = generatePassword(20);
      expect(pw).not.toMatch(/[01OIl]/);
    }
  });

  it("never emits shell-unsafe characters", () => {
    for (let i = 0; i < 500; i++) {
      const pw = generatePassword(20);
      expect(pw).not.toMatch(/["'`\\$ \n\t]/);
    }
  });

  it("produces different passwords on successive calls", () => {
    const a = generatePassword(16);
    const b = generatePassword(16);
    expect(a).not.toEqual(b);
  });
});
