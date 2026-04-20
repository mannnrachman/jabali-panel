// JabaliFooter — brand footer rendered at the bottom of both admin and
// user shells via Refine's ThemedLayoutV2 Footer slot.
//
// Left: logo + "Jabali Panel" + tagline.
// Right: source link, copyright, and current version tag.
//
// The version string is imported from panel-ui/package.json at build
// time so bumping the SPA version there propagates automatically.
import { Layout, Tag, Typography } from "antd";

import { useThemeMode } from "../theme/ThemeModeContext";
import pkg from "../../package.json";

// Canonical source-code URL. Pointing at the Gitea mirror for now; swap
// to a GitHub URL here if the project gets mirrored publicly.
const SOURCE_URL = "https://git.linux-hosting.co.il/shukivaknin/jabali2";

export function JabaliFooter() {
  const { mode } = useThemeMode();
  const logoSrc =
    mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";
  const fadedColor = mode === "dark" ? "rgba(255,255,255,0.45)" : "rgba(0,0,0,0.45)";
  const dotColor = mode === "dark" ? "rgba(255,255,255,0.25)" : "rgba(0,0,0,0.25)";
  const year = new Date().getFullYear();

  return (
    <Layout.Footer
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 16,
        padding: "16px 24px",
        background: "transparent",
        borderTop:
          mode === "dark" ? "1px solid #303030" : "1px solid #f0f0f0",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <img
          src={logoSrc}
          alt="Jabali"
          style={{ height: 32, width: "auto", flexShrink: 0 }}
        />
        <div style={{ lineHeight: 1.3 }}>
          <Typography.Text strong style={{ display: "block", fontSize: 14 }}>
            Jabali Panel
          </Typography.Text>
          <Typography.Text style={{ fontSize: 12, color: fadedColor }}>
            Web Hosting Control Panel
          </Typography.Text>
        </div>
      </div>

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          fontSize: 13,
          color: fadedColor,
        }}
      >
        <Typography.Link
          href={SOURCE_URL}
          target="_blank"
          rel="noreferrer"
          style={{ color: fadedColor }}
        >
          GitHub
        </Typography.Link>
        <span style={{ color: dotColor }}>•</span>
        <span>© {year} Jabali</span>
        <Tag style={{ margin: 0 }}>v{pkg.version}</Tag>
      </div>
    </Layout.Footer>
  );
}
