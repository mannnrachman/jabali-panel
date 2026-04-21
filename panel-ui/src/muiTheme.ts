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
          // Sidebar Menu selected-row styling per operator. Red
          // accent on both algorithms, bg tuned per mode.
          //
          // AntD Menu uses separate tokens once you pass theme="dark"
          // at the component level — the `item*` set applies to the
          // light theme, while `darkItem*` targets the dark theme.
          // Setting only itemSelectedBg in dark mode silently no-ops
          // because the dark-theme CSS path never reads it.
          // Hover: keep the default text/icon color (the red accent is
          // reserved for the *selected* row) and only paint a subtle bg
          // so the item highlights without shouting. Same gray family
          // as the selected bg, one shade lighter so hover and selected
          // are still distinguishable.
          Menu:
            mode === "dark"
              ? {
                  darkItemSelectedBg: "#1f1f1f",
                  darkItemSelectedColor: "#ef4444",
                  darkItemHoverBg: "#1a1a1a",
                  darkItemHoverColor: "rgba(255, 255, 255, 0.85)",
                }
              : {
                  itemSelectedBg: "#f3f4f6",
                  itemSelectedColor: "#dc2626",
                  itemHoverBg: "#f9fafb",
                  itemHoverColor: "rgba(0, 0, 0, 0.88)",
                },
          // Tabs pick up the same red accent the selected sidebar row
          // uses so the "active thing on this page" color stays
          // consistent across chrome and content. inkBarColor paints
          // the underline; itemSelectedColor the active tab label;
          // itemHoverColor keeps the subtle hover emphasis.
          Tabs: {
            itemSelectedColor: mode === "dark" ? "#ef4444" : "#dc2626",
            inkBarColor: mode === "dark" ? "#ef4444" : "#dc2626",
            itemHoverColor: mode === "dark" ? "#ef4444" : "#dc2626",
            itemActiveColor: mode === "dark" ? "#ef4444" : "#dc2626",
          },
        },
      },
    }),
    [mode],
  );

export default useMuiTheme;
