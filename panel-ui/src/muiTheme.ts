// muiTheme.ts — AntD ConfigProvider configuration.
//
// Intentionally minimal: just the algorithm switch so dark mode
// engages, and nothing else. AntD's built-in seed tokens drive every
// color/size/radius/shadow — no MUI palette, no component-level
// overrides, no wave ripple, no antd-style classNames. The panel
// renders with stock AntD chrome.
import { useMemo } from "react";
import { theme } from "antd";
import type { ConfigProviderProps } from "antd";

import type { ThemeMode } from "./theme/ThemeModeContext";

const useMuiTheme = (mode: ThemeMode): ConfigProviderProps =>
  useMemo<ConfigProviderProps>(
    () => ({
      theme: {
        algorithm:
          mode === "dark" ? theme.darkAlgorithm : theme.defaultAlgorithm,
        components: {
          // The Sider's collapse trigger bar at the bottom otherwise
          // paints its own hardcoded color (navy in dark, white in
          // light) which clashes with our Sider body bg pinned to
          // `colorBgLayout`. Transparent lets the Sider bg show
          // through so the trigger blends with the sidebar.
          //
          // Light-mode bodyBg = Tailwind gray-50 (#f9fafb) so the main
          // content surface matches the sidebar (also gray-50). Cards
          // on top stay white (colorBgContainer) and read as raised.
          Layout: {
            triggerBg: "transparent",
            ...(mode === "light" ? { bodyBg: "#f9fafb" } : {}),
          },
          // Light-mode sidebar selected-row bg = Tailwind gray-100 per
          // operator. Dark mode keeps the algorithm default (which
          // already pairs well with colorBgLayout). AntD Menu
          // itemSelectedColor stays default so the active text keeps
          // its primary-color emphasis.
          ...(mode === "light"
            ? { Menu: { itemSelectedBg: "#f3f4f6" } }
            : {}),
        },
      },
    }),
    [mode],
  );

export default useMuiTheme;
