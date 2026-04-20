// muiTheme.ts — AntD v6 ConfigProvider configuration modeled on the
// MUI-flavored reference demo. Light mode is a near-verbatim port of
// the upstream example: MUI palette tokens, elevation shadows, 500-
// weight uppercase buttons, Roboto text inputs, and a Material-style
// inset ripple wired via ConfigProvider's `wave` slot. Dark mode
// piggybacks on AntD's darkAlgorithm for surface/text derivation and
// keeps only brand primary + sizing to avoid maintaining a second
// 50-token palette.
import { useMemo } from "react";
import raf from "@rc-component/util/lib/raf";
import { theme } from "antd";
import type { ConfigProviderProps, GetProp } from "antd";
import { createStyles } from "antd-style";
import clsx from "clsx";

import type { ThemeMode } from "./theme/ThemeModeContext";

type WaveConfig = GetProp<ConfigProviderProps, "wave">;

// Base font size applied to both light and dark tokens. Upstream MUI
// sits at 14; we bump to 16 to match iOS/macOS body type. Tables are
// pinned back to 14 via the per-component override below.
const baseFontSize = 16;

// ---------------------------------------------------------------------------
// Inset ripple — Material-style wave that sweeps a translucent white
// dot out from the click point inside the Button's own bounds. Only
// applied to Button; other components keep AntD's default outset wave.
// ---------------------------------------------------------------------------
const createHolder = (node: HTMLElement) => {
  const { borderWidth } = getComputedStyle(node);
  const borderWidthNum = Number.parseInt(borderWidth, 10) || 0;
  const div = document.createElement("div");
  div.style.position = "absolute";
  div.style.inset = `-${borderWidthNum}px`;
  div.style.borderRadius = "inherit";
  div.style.background = "transparent";
  div.style.zIndex = "999";
  div.style.pointerEvents = "none";
  div.style.overflow = "hidden";
  node.appendChild(div);
  return div;
};

const createDot = (
  holder: HTMLElement,
  color: string,
  left: number,
  top: number,
  size = 0,
) => {
  const dot = document.createElement("div");
  dot.style.position = "absolute";
  dot.style.insetInlineStart = `${left}px`;
  dot.style.top = `${top}px`;
  dot.style.width = `${size}px`;
  dot.style.height = `${size}px`;
  dot.style.borderRadius = "50%";
  dot.style.background = color;
  dot.style.transform = "translate3d(-50%, -50%, 0)";
  dot.style.transition = "all 1s ease-out";
  holder.appendChild(dot);
  return dot;
};

const showInsetEffect: WaveConfig["showEffect"] = (
  node,
  { event, component },
) => {
  if (component !== "Button") return;
  const holder = createHolder(node);
  const rect = holder.getBoundingClientRect();
  const left = event.clientX - rect.left;
  const top = event.clientY - rect.top;
  const dot = createDot(holder, "rgba(255, 255, 255, 0.65)", left, top);
  raf(() => {
    dot.ontransitionend = () => holder.remove();
    dot.style.width = "200px";
    dot.style.height = "200px";
    dot.style.opacity = "0";
  });
};

// ---------------------------------------------------------------------------
// Component classNames — MUI-style button/input/select chrome (uppercase
// labels, Roboto text, elevation shadows) applied via antd-style's
// createStyles so the CSS gets scoped + hashed. Only used in light mode;
// dark mode opts out so AntD's darkAlgorithm surfaces don't fight
// hand-rolled MUI colors.
// ---------------------------------------------------------------------------
const useLightStyles = createStyles(({ css }) => ({
  buttonPrimary: css({
    backgroundColor: "#1976d2",
    color: "#ffffff",
    border: "none",
    fontWeight: 500,
    textTransform: "uppercase",
    letterSpacing: "0.02857em",
    boxShadow:
      "0px 3px 1px -2px rgba(0,0,0,0.2), 0px 2px 2px 0px rgba(0,0,0,0.14), 0px 1px 5px 0px rgba(0,0,0,0.12)",
    transition: "all 0.2s cubic-bezier(0.4, 0, 0.2, 1)",
  }),
  buttonDefault: css({
    backgroundColor: "#ffffff",
    color: "rgba(0, 0, 0, 0.87)",
    border: "1px solid rgba(0, 0, 0, 0.23)",
    fontWeight: 500,
    textTransform: "uppercase",
    letterSpacing: "0.02857em",
    transition: "all 0.2s cubic-bezier(0.4, 0, 0.2, 1)",
  }),
  buttonDanger: css({
    backgroundColor: "#d32f2f",
    color: "#ffffff",
    border: "none",
    fontWeight: 500,
    textTransform: "uppercase",
    letterSpacing: "0.02857em",
    boxShadow:
      "0px 3px 1px -2px rgba(0,0,0,0.2), 0px 2px 2px 0px rgba(0,0,0,0.14), 0px 1px 5px 0px rgba(0,0,0,0.12)",
  }),
  inputRoot: css({
    borderColor: "rgba(0, 0, 0, 0.23)",
    transition: "all 0.2s cubic-bezier(0.4, 0, 0.2, 1)",
  }),
  inputElement: css({
    color: "rgba(0, 0, 0, 0.87)",
    fontFamily: '"Roboto", "Helvetica", "Arial", sans-serif',
  }),
  inputError: css({
    borderColor: "#d32f2f",
  }),
  selectRoot: css({
    borderColor: "rgba(0, 0, 0, 0.23)",
    fontFamily: '"Roboto", "Helvetica", "Arial", sans-serif',
  }),
}));

// ---------------------------------------------------------------------------
// Tokens — Light is the upstream MUI-flavored palette verbatim. Dark
// keeps brand primary + sizing and lets AntD's darkAlgorithm derive
// surface/text/border so the two modes stay in lockstep without
// hand-maintaining a second 50-token palette.
// ---------------------------------------------------------------------------
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

// Tables pin to fontSize 14 regardless of mode — 16 makes rows feel
// heavier than AntD's stock list pages.
const tableTokens = {
  fontSize: 14,
  cellPaddingBlock: 8,
};

const buttonComponentTokens = {
  primaryShadow:
    "0px 3px 1px -2px rgba(0,0,0,0.2), 0px 2px 2px 0px rgba(0,0,0,0.14), 0px 1px 5px 0px rgba(0,0,0,0.12)",
  defaultShadow:
    "0px 3px 1px -2px rgba(0,0,0,0.2), 0px 2px 2px 0px rgba(0,0,0,0.14), 0px 1px 5px 0px rgba(0,0,0,0.12)",
  dangerShadow:
    "0px 3px 1px -2px rgba(0,0,0,0.2), 0px 2px 2px 0px rgba(0,0,0,0.14), 0px 1px 5px 0px rgba(0,0,0,0.12)",
  fontWeight: 500,
  defaultBorderColor: "rgba(0, 0, 0, 0.23)",
  defaultColor: "rgba(0, 0, 0, 0.87)",
  defaultBg: "#ffffff",
  defaultHoverBg: "rgba(25, 118, 210, 0.04)",
  defaultHoverBorderColor: "rgba(0, 0, 0, 0.23)",
  paddingInline: 16,
  paddingBlock: 6,
  contentFontSize: 14,
  borderRadius: 4,
};

const lightComponents = {
  Button: buttonComponentTokens,
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

// ---------------------------------------------------------------------------
// Shell surface tokens — consumed by layouts (sider, header, content)
// via useShellTokens(). Keeps hex values out of components.
// ---------------------------------------------------------------------------
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
  // createStyles must be called unconditionally (hook rules). Class
  // names are only attached in light mode; dark mode passes undefined
  // so AntD's darkAlgorithm surfaces render cleanly.
  const { styles } = useLightStyles();

  return useMemo<ConfigProviderProps>(
    () => ({
      theme: {
        algorithm:
          mode === "dark" ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: mode === "dark" ? darkTokens : lightTokens,
        components: mode === "dark" ? darkComponents : lightComponents,
      },
      wave: mode === "dark" ? undefined : { showEffect: showInsetEffect },
      button:
        mode === "dark"
          ? undefined
          : {
              classNames: ({ props }) => ({
                root: clsx(
                  props.type === "primary" && styles.buttonPrimary,
                  props.type === "default" && styles.buttonDefault,
                  props.danger && styles.buttonDanger,
                ),
              }),
            },
      input:
        mode === "dark"
          ? undefined
          : {
              classNames: ({ props }) => ({
                root: clsx(
                  styles.inputRoot,
                  props.status === "error" && styles.inputError,
                ),
                input: styles.inputElement,
              }),
            },
      select:
        mode === "dark"
          ? undefined
          : {
              classNames: {
                root: styles.selectRoot,
              },
            },
    }),
    [mode, styles],
  );
};

export const useShellTokens = (mode: ThemeMode): ShellTokens =>
  useMemo(() => shellTokensByMode(mode), [mode]);

export default useMuiTheme;
