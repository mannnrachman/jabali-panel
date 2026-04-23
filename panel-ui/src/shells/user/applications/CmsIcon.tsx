import { SiWordpress, SiWikipedia, SiDrupal, SiJoomla, SiPhpbb, SiPrestashop } from "react-icons/si";
import { FaOpencart } from "react-icons/fa6";
import { BookOutlined } from "@icons";
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
  drupal: "#0678BE",
  joomla: "#F44321",
  phpbb: "#26477F",
  opencart: "#2AC2EF",
  prestashop: "#DF0067",
};

// CmsIcon renders the brand logo for an app_type. Unknown app_type falls
// back to the AntD BookOutlined generic icon.
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
  if (key === "opencart") {
    return <FaOpencart style={style} />;
  }
  if (key === "prestashop") {
    return <SiPrestashop style={style} />;
  }
  return <BookOutlined style={style} />;
}
