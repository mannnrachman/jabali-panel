// RowActionButton — the canonical per-row action button. Filled style,
// required icon, default color="primary". Pairs with RowDeleteButton
// for destructive actions; together they're the only button shapes
// that should appear inside a table's "Actions" column.
//
// Why filled (AntD `variant="filled"`): the previous mix of
// `type="text"`, `type="link"`, and the default outlined `type="default"`
// across tables made the same row look different from page to page.
// Filled buttons read as a single visual class — green = primary
// action, red = destructive — and the row scans cleanly even on
// dense tables.
//
// Icon is REQUIRED. The Lucide shim at `@icons` covers every action
// we ship; if a row really has no semantic icon, pick one from
// MoreOutlined / SettingsOutlined and revisit when the action gets
// a name.
//
// For destructive actions (Delete, Reset, Revoke), use RowDeleteButton
// — it bundles the AntD Popconfirm + danger styling. RowActionButton
// can also render danger via `danger`, but it does NOT pop a confirm
// — opt for RowDeleteButton when the action is irreversible.

import type { ButtonProps } from "antd";
import { Button } from "antd";
import type { ReactNode } from "react";

export interface RowActionButtonProps
  extends Omit<ButtonProps, "type" | "variant" | "color" | "icon"> {
  /** Required — every row action carries an icon. */
  icon: ReactNode;
  /** Optional label; omit for icon-only buttons. */
  children?: ReactNode;
  /** Override the default `color="primary"` (e.g. `default`). */
  color?: ButtonProps["color"];
}

export function RowActionButton({
  icon,
  children,
  color = "primary",
  ...rest
}: RowActionButtonProps) {
  return (
    <Button
      variant="filled"
      color={rest.danger ? "danger" : color}
      icon={icon}
      {...rest}
    >
      {children}
    </Button>
  );
}
