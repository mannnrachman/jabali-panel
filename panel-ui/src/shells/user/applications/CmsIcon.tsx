import { SiWordpress, SiWikipedia, SiDrupal, SiJoomla, SiPhpbb, SiGrav, SiFreshrss, SiMatomo, SiPrestashop, SiMoodle } from "react-icons/si";
import { FaOpencart } from "react-icons/fa6";
import { BookOutlined, AppstoreOutlined } from "@ant-design/icons";
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
  freshrss: "#88BC23",
  matomo: "#3152A0",
  concrete: "#34669B",
  opencart: "#2AC2EF",
  abantecart: "#1B8AC4",
  prestashop: "#DF0067",
  // Backdrop CMS uses Drupal's blue palette in branding; pick a slightly
  // lighter shade to avoid colliding with our existing Drupal entry.
  backdrop: "#0288D1",
  moodle: "#F98012",
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
  if (key === "freshrss") {
    return <SiFreshrss style={style} />;
  }
  if (key === "matomo") {
    return <SiMatomo style={style} />;
  }
  if (key === "opencart") {
    return <FaOpencart style={style} />;
  }
  if (key === "abantecart") {
    // Simple Icons doesn't ship an AbanteCart entry; fall back to the
    // OpenCart cart glyph (same e-commerce visual cue) in AbanteCart's
    // brand blue so the colour disambiguates the two in the catalog.
    return <FaOpencart style={style} />;
  }
  if (key === "prestashop") {
    return <SiPrestashop style={style} />;
  }
  if (key === "moodle") {
    return <SiMoodle style={style} />;
  }
  if (key === "backdrop") {
    // Simple Icons doesn't ship a Backdrop entry. Fall back to the
    // generic appstore glyph in the brand blue.
    return <AppstoreOutlined style={style} />;
  }
  if (key === "concrete") {
    // Simple Icons doesn't ship a Concrete CMS entry; fall back to a
    // generic appstore glyph in the brand blue.
    return <AppstoreOutlined style={style} />;
  }
  if (key === "dokuwiki") {
    return <BookOutlined style={style} />;
  }
  return <BookOutlined style={style} />;
}
