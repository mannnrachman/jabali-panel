import { SiWordpress, SiWikipedia, SiDrupal, SiJoomla, SiPhpbb, SiGrav } from "react-icons/si";
import { BookOutlined } from "@ant-design/icons";
import type { CSSProperties } from "react";

interface CmsIconProps {
  appType: string | undefined;
  size?: number;
}

// Brand colors for the CMS logos. Falling back to currentColor for the
// generic icon so themed dark/light backgrounds keep contrast without a
// per-mode override.
const BRAND_COLOR: Record<string, string> = {
  wordpress: "#21759B",
  mediawiki: "#990000",
  dokuwiki: "#CC0000",
  drupal: "#0678BE",
  joomla: "#F44321",
  phpbb: "#26477F",
  grav: "#221E1F",
};

// CmsIcon renders the brand logo for an app_type. WordPress and
// MediaWiki ship as Simple Icons (react-icons/si). DokuWiki doesn't
// have a Simple Icons entry so we fall back to AntD BookOutlined in the
// DokuWiki red — clearly indicates "wiki" without fabricating the
// official wordmark.
export function CmsIcon({ appType, size = 18 }: CmsIconProps) {
  const key = appType || "wordpress";
  const color = BRAND_COLOR[key];
  const style: CSSProperties = {
    fontSize: size,
    width: size,
    height: size,
    color,
    flexShrink: 0,
  };

  if (key === "wordpress") {
    return <SiWordpress style={style} />;
  }
  if (key === "mediawiki") {
    return <SiWikipedia style={style} />;
  }
  if (key === "drupal") {
    return <SiDrupal style={style} />;
  }
  if (key === "joomla") {
    return <SiJoomla style={style} />;
  }
  if (key === "phpbb") {
    return <SiPhpbb style={style} />;
  }
  if (key === "grav") {
    return <SiGrav style={style} />;
  }
  if (key === "dokuwiki") {
    return <BookOutlined style={style} />;
  }
  return <BookOutlined style={style} />;
}
