// Test setup — runs before every test file via vitest.setupFiles.
//
// Keeps to a minimum:
//   - jest-dom matchers (toBeInTheDocument, etc.)
//   - window.matchMedia polyfill (AntD uses it for responsive bits;
//     happy-dom doesn't ship it)
import "@testing-library/jest-dom/vitest";

if (typeof window !== "undefined" && !window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}
