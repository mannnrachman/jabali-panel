// ThemeToggle — one-click sun/moon switch used in both shell headers.
import { MoonOutlined, SunOutlined } from "@ant-design/icons";
import { Button, Tooltip } from "antd";

import { useThemeMode } from "../theme/ThemeModeContext";

export function ThemeToggle() {
  const { mode, toggle } = useThemeMode();
  const next = mode === "dark" ? "light" : "dark";

  return (
    <Tooltip title={`Switch to ${next} mode`}>
      <Button
        type="link"
        aria-label={`Switch to ${next} mode`}
        icon={mode === "dark" ? <SunOutlined /> : <MoonOutlined />}
        onClick={toggle}
      />
    </Tooltip>
  );
}
