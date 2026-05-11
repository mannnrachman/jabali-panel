import { Card, Progress, Table, Tag } from "antd";
import { HddOutlined } from "@ant-design/icons";

import { humanBytes } from "../../../utils/bytes";
import type { Partition } from "../../../hooks/useServerStatus";

interface Props {
  partitions: Partition[];
}

export function DisksTable({ partitions }: Props) {
  return (
    <Card title={<><HddOutlined /> Disks</>} size="small">
      <Table<Partition>
        rowKey="mount_point"
        size="small"
        dataSource={partitions}
        pagination={false}
        scroll={{ x: "max-content" }}
        rowClassName={(r) => {
          const pct = pctOf(r);
          if (pct >= 95) return "row-disk-critical";
          if (pct >= 80) return "row-disk-warning";
          return "";
        }}
        columns={[
          { title: "Mount", dataIndex: "mount_point" },
          {
            title: "Used",
            render: (_, r) => (
              <div style={{ minWidth: 200 }}>
                <Progress
                  percent={Math.round(pctOf(r))}
                  size="small"
                  strokeColor={diskColor(pctOf(r))}
                />
                <span style={{ fontSize: 11 }}>
                  {humanBytes(r.used_bytes)} / {humanBytes(r.total_bytes)}
                </span>
              </div>
            ),
          },
          {
            title: "Free",
            render: (_, r) => humanBytes(r.free_bytes),
          },
          {
            title: "Status",
            render: (_, r) => {
              const p = pctOf(r);
              if (p >= 95) return <Tag color="red">critical</Tag>;
              if (p >= 80) return <Tag color="orange">warning</Tag>;
              return <Tag color="green">healthy</Tag>;
            },
          },
        ]}
      />
    </Card>
  );
}

function pctOf(p: Partition): number {
  if (!p.total_bytes) return 0;
  return (p.used_bytes / p.total_bytes) * 100;
}

function diskColor(pct: number): string {
  if (pct >= 95) return "#cf1322";
  if (pct >= 80) return "#fa8c16";
  return "#52c41a";
}

