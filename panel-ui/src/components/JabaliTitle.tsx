// JabaliTitle — sidebar + header brand lockup. Swaps between the light
// and dark SVG variants based on the current theme mode, and collapses
// to icon-only when Refine's ThemedLayoutV2 collapses the sider.
//
// The SVGs live in public/images/ and are served directly by Vite / the
// Go embed.go fallback. Keeping them as raw files (rather than bundling
// through an SVG-as-JSX plugin) means we can swap the source without a
// rebuild and avoid the extra loader dependency.
import { useThemeMode } from "../theme/ThemeModeContext";

interface JabaliTitleProps {
  collapsed?: boolean;
  text?: string;
}

export function JabaliTitle({ collapsed, text = "Jabali Panel" }: JabaliTitleProps) {
  const { mode } = useThemeMode();
  const src = mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "8px 4px",
      }}
    >
      <img
        src={src}
        alt="Jabali"
        style={{ height: 28, width: "auto", flexShrink: 0 }}
      />
      {!collapsed && (
        <span style={{ fontSize: 16, fontWeight: 600, letterSpacing: 0.5 }}>
          {text}
        </span>
      )}
    </div>
  );
}
