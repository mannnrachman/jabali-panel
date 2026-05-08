// EngineTag — shared visual badge for the database engine column.
// Renders an AntD Tag with the engine's brand colour palette plus
// the upstream brand icon (sourced via simple-icons through
// react-icons/si). Used by both the user databases list and the
// admin database-users list so the two stay visually consistent.
//
// Design choices:
//   - MariaDB:    teal/blue ("cyan") + SiMariadb (seal mark)
//   - PostgreSQL: indigo ("geekblue") + SiPostgresql (elephant)
// Both colours are in AntD's preset palette so dark-mode contrast
// is handled by the design tokens automatically.

import { Tag } from "antd";
import { SiMariadb, SiPostgresql } from "react-icons/si";

type EngineKey = "mariadb" | "postgres";

const META: Record<
  EngineKey,
  { label: string; color: string; Icon: typeof SiMariadb }
> = {
  mariadb: { label: "MariaDB", color: "cyan", Icon: SiMariadb },
  postgres: { label: "PostgreSQL", color: "geekblue", Icon: SiPostgresql },
};

export interface EngineTagProps {
  /** Engine identifier from the API (case-insensitive). */
  engine?: string | null;
}

export function EngineTag({ engine }: EngineTagProps) {
  const key = ((engine ?? "mariadb").toLowerCase() as EngineKey);
  const meta = META[key];
  if (!meta) {
    return <Tag>{engine ?? "unknown"}</Tag>;
  }
  const { label, color, Icon } = meta;
  return (
    <Tag
      color={color}
      icon={
        <Icon
          style={{
            verticalAlign: "-0.15em",
            marginInlineEnd: 4,
          }}
        />
      }
    >
      {label}
    </Tag>
  );
}
