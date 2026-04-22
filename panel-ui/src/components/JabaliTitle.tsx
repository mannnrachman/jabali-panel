// JabaliTitle — brand lockup (SVG logo + wordmark) at the top of the Sider.
//
// The SVG swaps between light/dark variants to stay legible against the
// current sider background. The wordmark uses plain AntD Typography.Title
// defaults (no hand-picked font size, weight, letter-spacing, or color).
// When the Sider collapses we render only the logo.
import { Typography } from "antd";
import { useThemeMode } from "../theme/ThemeModeContext";

interface JabaliTitleProps {
  collapsed?: boolean;
  text?: string;
  /** Show the "Jabali" wordmark next to the logo. Defaults to true.
   * JabaliHeader passes false below sm so the logo alone keeps the
   * header readable on xs phones. */
  showWordmark?: boolean;
}

export function JabaliTitle({
  collapsed = false,
  text = "Jabali",
  showWordmark = true,
}: JabaliTitleProps) {
  const { mode } = useThemeMode();
  const src = mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
      <img
        src={src}
        alt="Jabali"
        style={{ height: 32, width: "auto", flexShrink: 0 }}
      />
      {!collapsed && showWordmark && (
        <Typography.Title level={2} style={{ margin: 0 }}>
          {text}
        </Typography.Title>
      )}
    </div>
  );
}
