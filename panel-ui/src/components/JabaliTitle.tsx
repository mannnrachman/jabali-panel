// JabaliTitle — brand text at the top of the Sider.
//
// Plain AntD Typography — no SVG logo, no hand-picked font sizes,
// weights, colors or letter-spacing. Renders as nothing when the
// Sider is collapsed so the icon-only menu doesn't fight a stub.
import { Typography } from "antd";

interface JabaliTitleProps {
  collapsed?: boolean;
  text?: string;
}

export function JabaliTitle({ collapsed = false, text = "Jabali" }: JabaliTitleProps) {
  if (collapsed) return null;
  return (
    <Typography.Title level={4} style={{ margin: 0 }}>
      {text}
    </Typography.Title>
  );
}
