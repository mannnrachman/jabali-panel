// JabaliFooter — brand footer rendered at the bottom of both shells.
//
// Left: logo + "Jabali Panel" + tagline.
// Right: source link, copyright, and current version tag.
//
// The version string is imported from panel-ui/package.json at build
// time so bumping the SPA version there propagates automatically.
import { Layout, Space, Tag, Typography } from "antd";

import { useThemeMode } from "../theme/ThemeModeContext";
import pkg from "../../package.json";

// Canonical source-code URL. Pointing at the Gitea mirror for now; swap
// to a GitHub URL here if the project gets mirrored publicly.
const SOURCE_URL = "https://git.linux-hosting.co.il/shukivaknin/jabali2";

export function JabaliFooter() {
  const { mode } = useThemeMode();
  const logoSrc =
    mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";
  const year = new Date().getFullYear();

  return (
    <Layout.Footer
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 16,
      }}
    >
      <Space size={12}>
        <img
          src={logoSrc}
          alt="Jabali"
          style={{ height: 32, width: "auto", flexShrink: 0 }}
        />
        <div>
          <Typography.Text strong style={{ display: "block" }}>
            Jabali Panel
          </Typography.Text>
          <Typography.Text type="secondary">
            Web Hosting Control Panel
          </Typography.Text>
        </div>
      </Space>

      <Space size={12}>
        <Typography.Text type="secondary">
          <a href={SOURCE_URL} target="_blank" rel="noreferrer">
            GitHub
          </a>
        </Typography.Text>
        <Typography.Text type="secondary">·</Typography.Text>
        <Typography.Text type="secondary">© {year} Jabali</Typography.Text>
        <Tag>v{pkg.version}</Tag>
      </Space>
    </Layout.Footer>
  );
}
