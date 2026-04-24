// JabaliTitle — brand lockup (SVG logo + wordmark) at the top of the Sider.
//
// The SVG swaps between light/dark variants to stay legible against the
// current sider background. Operator branding (M28) overrides both the
// logo source and the wordmark text via useBranding; empty brand text
// falls back to "Jabali".
import { Typography } from "antd";
import { useThemeMode } from "../theme/ThemeModeContext";
import { logoURL, useBranding } from "../hooks/useBranding";

interface JabaliTitleProps {
  collapsed?: boolean;
  text?: string;
  /** Show the wordmark next to the logo. Defaults to true.
   * JabaliHeader passes false below sm so the logo alone keeps the
   * header readable on xs phones. */
  showWordmark?: boolean;
}

export function JabaliTitle({
  collapsed = false,
  text,
  showWordmark = true,
}: JabaliTitleProps) {
  const { mode } = useThemeMode();
  const { brandText, hasLogoLight, hasLogoDark } = useBranding();
  const variant: "light" | "dark" = mode === "dark" ? "dark" : "light";
  const src = logoURL(variant, variant === "dark" ? hasLogoDark : hasLogoLight);
  const label = text ?? brandText ?? "Jabali";

  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
      <img
        src={src}
        alt={label || "Jabali"}
        style={{ height: 32, width: "auto", flexShrink: 0 }}
      />
      {!collapsed && showWordmark && (
        <Typography.Title level={2} style={{ margin: 0 }}>
          {label || "Jabali"}
        </Typography.Title>
      )}
    </div>
  );
}
