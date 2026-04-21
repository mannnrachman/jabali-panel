// JabaliFooter — brand footer rendered at the bottom of both shells.
//
// Left: logo + "Jabali Panel" + tagline.
// Right: source link, copyright, and current version tag.
//
// The version string is imported from panel-ui/package.json at build
// time so bumping the SPA version there propagates automatically.
import { GithubOutlined } from "@ant-design/icons";
import { Layout, Space, Typography } from "antd";

import { useThemeMode } from "../theme/ThemeModeContext";
import pkg from "../../package.json";

// Canonical source-code URL. Pointing at the Gitea mirror for now; swap
// to a GitHub URL here if the project gets mirrored publicly.
const SOURCE_URL = "https://git.linux-hosting.co.il/shukivaknin/jabali2";
const WEBSITE_URL = "https://jabali-panel.com/";

export function JabaliFooter() {
  const { mode } = useThemeMode();
  const logoSrc =
    mode === "dark" ? "/images/jabali_logo_dark.svg" : "/images/jabali_logo.svg";

  return (
    <Layout.Footer
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        gap: 16,
        // AntD's Footer default padding is 24px 50px — the 24 top/bottom
        // leaves a visible gap above the logo on short list views. Tight
        // to 8px vertical; keep 24px horizontal to match Content padding.
        padding: "8px 24px",
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
            <a
              href={WEBSITE_URL}
              target="_blank"
              rel="noreferrer"
              style={{ color: "inherit" }}
            >
              Jabali Panel
            </a>
          </Typography.Text>
          <Typography.Text type="secondary">
            Web Hosting Control Panel
          </Typography.Text>
        </div>
      </Space>

      <Space size={12}>
        <a
          href={SOURCE_URL}
          target="_blank"
          rel="noreferrer"
          aria-label="Source code"
          style={{ color: "inherit", display: "inline-flex", alignItems: "center" }}
        >
          <GithubOutlined style={{ fontSize: 18 }} />
        </a>
        <Typography.Text type="secondary">·</Typography.Text>
        <Typography.Text type="secondary">
          <a
            href="https://www.gnu.org/licenses/agpl-3.0.html"
            target="_blank"
            rel="noreferrer"
            style={{ color: "inherit" }}
          >
            AGPL-3.0
          </a>
        </Typography.Text>
        <Typography.Text strong>v{pkg.version}</Typography.Text>
      </Space>
    </Layout.Footer>
  );
}
