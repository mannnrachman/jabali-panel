// JabaliTitle — brand lockup (SVG + wordmark), theme-aware.
//
// Chooses the light or dark SVG variant from the current theme mode so
// it reads correctly against AntD's header/content backgrounds. Text
// colour inherits from the parent so it matches the header tokens.
import { useThemeMode } from "../theme/ThemeModeContext";

interface JabaliTitleProps {
  text?: string;
  /** When true, hide the wordmark and render only the icon. */
  iconOnly?: boolean;
}

export function JabaliTitle({ text = "Jabali Panel", iconOnly = false }: JabaliTitleProps) {
  const { mode } = useThemeMode();
  // Light theme → default-fill (dark) SVG reads on white bg.
  // Dark theme  → white-fill SVG reads on near-black bg.
  const src = mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";

  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 10,
      }}
    >
      <img
        src={src}
        alt="Jabali"
        style={{ height: 28, width: "auto", flexShrink: 0 }}
      />
      {!iconOnly && (
        <span style={{ fontSize: 16, fontWeight: 600, letterSpacing: 0.5 }}>
          {text}
        </span>
      )}
    </div>
  );
}
