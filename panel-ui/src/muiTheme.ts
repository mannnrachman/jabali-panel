import { useMemo } from "react";
import { theme } from "antd";
import type { ConfigProviderProps } from "antd";

import type { ThemeMode } from "./theme/ThemeModeContext";

// Base font size applied to both light and dark tokens. AntD's default is
// 14; 16 matches iOS/macOS body type and lifts heading/form/tag sizes via
// the algorithm's seed-derivation. Bump here, not per-token, to keep the
// two palettes in lockstep.
const baseFontSize = 16;

// Light palette — MUI-flavoured tokens kept verbatim so light mode stays
// pixel-identical to the pre-dark-mode design.
const lightTokens = {
  fontSize: baseFontSize,
  colorPrimary: "#1976d2",
  colorSuccess: "#2e7d32",
  colorWarning: "#ed6c02",
  colorError: "#d32f2f",
  colorInfo: "#0288d1",
  colorTextBase: "#212121",
  colorBgBase: "#fafafa",
  colorPrimaryBg: "#e3f2fd",
  colorPrimaryBgHover: "#bbdefb",
  colorPrimaryBorder: "#90caf9",
  colorPrimaryBorderHover: "#64b5f6",
  colorPrimaryHover: "#42a5f5",
  colorPrimaryActive: "#1565c0",
  colorPrimaryText: "#1976d2",
  colorPrimaryTextHover: "#42a5f5",
  colorPrimaryTextActive: "#1565c0",
  colorSuccessBg: "#e8f5e9",
  colorSuccessBgHover: "#c8e6c9",
  colorSuccessBorder: "#a5d6a7",
  colorSuccessBorderHover: "#81c784",
  colorSuccessHover: "#4caf50",
  colorSuccessActive: "#1b5e20",
  colorSuccessText: "#2e7d32",
  colorSuccessTextHover: "#4caf50",
  colorSuccessTextActive: "#1b5e20",
  colorWarningBg: "#fff3e0",
  colorWarningBgHover: "#ffe0b2",
  colorWarningBorder: "#ffcc02",
  colorWarningBorderHover: "#ffb74d",
  colorWarningHover: "#ff9800",
  colorWarningActive: "#e65100",
  colorWarningText: "#ed6c02",
  colorWarningTextHover: "#ff9800",
  colorWarningTextActive: "#e65100",
  colorErrorBg: "#ffebee",
  colorErrorBgHover: "#ffcdd2",
  colorErrorBorder: "#ef9a9a",
  colorErrorBorderHover: "#e57373",
  colorErrorHover: "#ef5350",
  colorErrorActive: "#c62828",
  colorErrorText: "#d32f2f",
  colorErrorTextHover: "#ef5350",
  colorErrorTextActive: "#c62828",
  colorInfoBg: "#e1f5fe",
  colorInfoBgHover: "#b3e5fc",
  colorInfoBorder: "#81d4fa",
  colorInfoBorderHover: "#4fc3f7",
  colorInfoHover: "#03a9f4",
  colorInfoActive: "#01579b",
  colorInfoText: "#0288d1",
  colorInfoTextHover: "#03a9f4",
  colorInfoTextActive: "#01579b",
  colorText: "rgba(33, 33, 33, 0.87)",
  colorTextSecondary: "rgba(33, 33, 33, 0.6)",
  colorTextTertiary: "rgba(33, 33, 33, 0.38)",
  colorTextQuaternary: "rgba(33, 33, 33, 0.26)",
  colorTextDisabled: "rgba(33, 33, 33, 0.38)",
  colorBgContainer: "#ffffff",
  colorBgElevated: "#ffffff",
  colorBgLayout: "#f5f5f5",
  colorBgSpotlight: "rgba(33, 33, 33, 0.85)",
  colorBgMask: "rgba(33, 33, 33, 0.5)",
  colorBorder: "#e0e0e0",
  colorBorderSecondary: "#eeeeee",
  borderRadius: 4,
  borderRadiusXS: 1,
  borderRadiusSM: 2,
  borderRadiusLG: 6,
  padding: 16,
  paddingSM: 8,
  paddingLG: 24,
  margin: 16,
  marginSM: 8,
  marginLG: 24,
  boxShadow:
    "0px 2px 1px -1px rgba(0,0,0,0.2),0px 1px 1px 0px rgba(0,0,0,0.14),0px 1px 3px 0px rgba(0,0,0,0.12)",
  boxShadowSecondary:
    "0px 3px 3px -2px rgba(0,0,0,0.2),0px 3px 4px 0px rgba(0,0,0,0.14),0px 1px 8px 0px rgba(0,0,0,0.12)",
};

// Dark palette — just the brand primary + sizing; AntD's darkAlgorithm
// fills in surface/text/border so we don't have to hand-maintain 50+
// tokens twice.
const darkTokens = {
  fontSize: baseFontSize,
  colorPrimary: "#1976d2",
  colorSuccess: "#2e7d32",
  colorWarning: "#ed6c02",
  colorError: "#d32f2f",
  colorInfo: "#0288d1",
  borderRadius: 4,
  borderRadiusXS: 1,
  borderRadiusSM: 2,
  borderRadiusLG: 6,
  padding: 16,
  paddingSM: 8,
  paddingLG: 24,
  margin: 16,
  marginSM: 8,
  marginLG: 24,
};

// Table-specific font overrides: the app-wide base is 16 (user preference
// for body text legibility) but tables at 16 make rows and headers feel
// heavier than AntD's stock look. Pin Table components back to AntD's
// native 14 so list pages feel compact and familiar.
const tableTokens = {
  fontSize: 14,
  cellPaddingBlock: 8,
};

const lightComponents = {
  Alert: { borderRadiusLG: 4 },
  Modal: { borderRadiusLG: 4 },
  Progress: {
    defaultColor: "#1976d2",
    remainingColor: "rgba(25, 118, 210, 0.12)",
  },
  Steps: { iconSize: 24 },
  Checkbox: { borderRadiusSM: 2 },
  Slider: {
    trackBg: "rgba(25, 118, 210, 0.26)",
    trackHoverBg: "rgba(25, 118, 210, 0.38)",
    handleSize: 20,
    handleSizeHover: 20,
    railSize: 4,
  },
  ColorPicker: { borderRadius: 4 },
  Table: tableTokens,
};

const darkComponents = {
  Alert: { borderRadiusLG: 4 },
  Modal: { borderRadiusLG: 4 },
  Steps: { iconSize: 24 },
  Checkbox: { borderRadiusSM: 2 },
  Slider: {
    handleSize: 20,
    handleSizeHover: 20,
    railSize: 4,
  },
  ColorPicker: { borderRadius: 4 },
  Table: tableTokens,
};

// Layout surface tokens (sider, header, content) per mode.  Shells pull
// these via useShellTokens() so nothing hardcodes hex values.
export interface ShellTokens {
  siderBg: string;
  siderHeaderColor: string;
  headerBg: string;
  contentBg: string;
  headerBorder: string;
}

const shellTokensByMode = (mode: ThemeMode): ShellTokens =>
  mode === "dark"
    ? {
        siderBg: "#0a0a0a",
        siderHeaderColor: "#ffffff",
        headerBg: "#141414",
        contentBg: "#000000",
        headerBorder: "1px solid #303030",
      }
    : {
        siderBg: "#001529",
        siderHeaderColor: "#ffffff",
        headerBg: "#ffffff",
        contentBg: "#f5f5f5",
        headerBorder: "1px solid #f0f0f0",
      };

const useMuiTheme = (mode: ThemeMode) => {
  return useMemo<ConfigProviderProps>(
    () => ({
      theme: {
        algorithm:
          mode === "dark" ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: mode === "dark" ? darkTokens : lightTokens,
        components: mode === "dark" ? darkComponents : lightComponents,
      },
    }),
    [mode],
  );
};

export const useShellTokens = (mode: ThemeMode): ShellTokens =>
  useMemo(() => shellTokensByMode(mode), [mode]);

export default useMuiTheme;
