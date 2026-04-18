// JabaliTitle — brand lockup (SVG + wordmark), theme-aware.
//
// Rendered in the Title slot of Refine's ThemedLayoutV2 (i.e. at the
// top-left of the Sider). Refine passes `collapsed: boolean` when the
// sider collapses; we shrink to icon-only in that state.
//
// Chooses the light or dark SVG variant from the current theme mode so
// it reads correctly against the sider background.
import { useThemeMode } from "../theme/ThemeModeContext";

interface JabaliTitleProps {
  /** Refine's TitleProps contract. */
  collapsed?: boolean;
  /** Override text. Defaults to the product name. */
  text?: string;
}

export function JabaliTitle({ collapsed = false, text = "Jabali Panel" }: JabaliTitleProps) {
  const { mode } = useThemeMode();
  const src = mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
        padding: "0 4px",
      }}
    >
      <img
        src={src}
        alt="Jabali"
        style={{ height: 28, width: "auto", flexShrink: 0 }}
      />
      {!collapsed && (
        <span
          style={{
            fontSize: 16,
            fontWeight: 600,
            letterSpacing: 0.5,
            color: mode === "dark" ? "#f5f5f5" : "#1f1f1f",
          }}
        >
          {text}
        </span>
      )}
    </div>
  );
}
