// ThemeModeContext — tracks whether the panel is in light or dark mode.
//
// Precedence:
//   1. If the user has toggled in-app, their choice wins (persisted in
//      localStorage under `jabali.themeMode`).
//   2. Otherwise, follow the OS `prefers-color-scheme` media query live —
//      if the OS switches light→dark at 8pm, the panel follows.
//
// A user toggle "pins" the mode: we stop listening to the media query
// until the stored preference is cleared. That mirrors the standard
// tri-state (Light / Dark / System) pattern with a two-state UI.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export type ThemeMode = "light" | "dark";

const STORAGE_KEY = "jabali.themeMode";
const MEDIA_QUERY = "(prefers-color-scheme: dark)";

const readStored = (): ThemeMode | null => {
  if (typeof window === "undefined") return null;
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    return v === "light" || v === "dark" ? v : null;
  } catch {
    return null;
  }
};

const readSystem = (): ThemeMode => {
  if (typeof window === "undefined" || !window.matchMedia) return "light";
  return window.matchMedia(MEDIA_QUERY).matches ? "dark" : "light";
};

interface ThemeModeContextValue {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
  toggle: () => void;
}

const ThemeModeContext = createContext<ThemeModeContextValue | null>(null);

export function ThemeModeProvider({ children }: { children: ReactNode }) {
  const [pinned, setPinned] = useState<boolean>(() => readStored() !== null);
  const [mode, setModeState] = useState<ThemeMode>(
    () => readStored() ?? readSystem(),
  );

  // Mirror mode onto <html data-theme> and <body data-theme> so the
  // pre-paint CSS in index.html keeps matching after in-app toggles
  // (prevents a flash of the wrong background on route changes).
  useEffect(() => {
    if (typeof document === "undefined") return;
    document.documentElement.setAttribute("data-theme", mode);
    document.body.setAttribute("data-theme", mode);
  }, [mode]);

  // Follow OS changes while the user hasn't pinned a preference.
  useEffect(() => {
    if (pinned) return;
    if (typeof window === "undefined" || !window.matchMedia) return;

    const mq = window.matchMedia(MEDIA_QUERY);
    const onChange = (e: MediaQueryListEvent) => {
      setModeState(e.matches ? "dark" : "light");
    };
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [pinned]);

  const setMode = useCallback((next: ThemeMode) => {
    setModeState(next);
    setPinned(true);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* storage disabled — keep in-memory */
    }
  }, []);

  const toggle = useCallback(() => {
    setMode(mode === "dark" ? "light" : "dark");
  }, [mode, setMode]);

  const value = useMemo<ThemeModeContextValue>(
    () => ({ mode, setMode, toggle }),
    [mode, setMode, toggle],
  );

  return (
    <ThemeModeContext.Provider value={value}>
      {children}
    </ThemeModeContext.Provider>
  );
}

export function useThemeMode(): ThemeModeContextValue {
  const ctx = useContext(ThemeModeContext);
  if (!ctx) {
    throw new Error("useThemeMode must be used inside ThemeModeProvider");
  }
  return ctx;
}
