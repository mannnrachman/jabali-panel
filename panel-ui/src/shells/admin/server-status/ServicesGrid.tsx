// ServicesGrid — per-service health table. v1 is read-only; the
// "Show service controls" toggle reveals Start/Stop/Restart buttons,
// off by default so a casual page view can't bring services down.
import { useState } from "react";
import { Button, Card, Popconfirm, Space, Switch, Table, Tag, message } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";
import type { ServiceDetail } from "../../../hooks/useServerStatus";

interface Props {
  services: ServiceDetail[];
}

export function ServicesGrid({ services }: Props) {
  const [showControls, setShowControls] = useState(false);
  const qc = useQueryClient();

  const ctl = useMutation({
    mutationFn: async ({ unit, action }: { unit: string; action: string }) => {
      // Strip ".service" suffix because the agent's service.* commands
      // expect bare names; the unit field carries the full id for
      // display.
      const name = unit.replace(/\.service$/, "");
      await apiClient.post(`/admin/services/${encodeURIComponent(name)}/${action}`);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "server-status"] });
      message.success("Done");
    },
    onError: (e: unknown) => {
      message.error(e instanceof Error ? e.message : "Service action failed");
    },
  });

  return (
    <Card
      title="Services"
      size="small"
      extra={
        <Space>
          <span style={{ fontSize: 12 }}>Show service controls</span>
          <Switch checked={showControls} onChange={setShowControls} size="small" />
        </Space>
      }
    >
      <Table<ServiceDetail>
        rowKey="unit"
        size="small"
        dataSource={services}
        pagination={false}
        scroll={{ x: "max-content" }}
        columns={[
          { title: "Unit", dataIndex: "unit" },
          {
            title: "Active",
            dataIndex: "active",
            render: (s: string) => <Tag color={activeColor(s)}>{s}</Tag>,
          },
          { title: "Sub", dataIndex: "sub" },
          {
            title: "Memory",
            dataIndex: "memory_bytes",
            render: (v: number) => humanBytes(v),
          },
          {
            title: "Tasks",
            dataIndex: "tasks",
            render: (v: number) => v || "—",
          },
          {
            title: "Uptime",
            dataIndex: "uptime_seconds",
            render: (s: number) => humanDuration(s),
          },
          ...(showControls
            ? [
                {
                  title: "Actions",
                  render: (_: unknown, r: ServiceDetail) => (
                    <Space size={4}>
                      <Popconfirm
                        title={`Restart ${r.unit}?`}
                        onConfirm={() => ctl.mutate({ unit: r.unit, action: "restart" })}
                      >
                        <Button size="small">Restart</Button>
                      </Popconfirm>
                      <Popconfirm
                        title={`Stop ${r.unit}?`}
                        onConfirm={() => ctl.mutate({ unit: r.unit, action: "stop" })}
                      >
                        <Button size="small" danger>Stop</Button>
                      </Popconfirm>
                      <Button size="small" onClick={() => ctl.mutate({ unit: r.unit, action: "start" })}>
                        Start
                      </Button>
                    </Space>
                  ),
                },
              ]
            : []),
        ]}
      />
    </Card>
  );
}

function activeColor(state: string): string {
  switch (state) {
    case "active":
      return "green";
    case "inactive":
    case "failed":
      return "red";
    case "activating":
    case "deactivating":
      return "orange";
    default:
      return "default";
  }
}

function humanBytes(b: number): string {
  if (!b) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = b;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}

function humanDuration(secs: number): string {
  if (!secs) return "—";
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}
