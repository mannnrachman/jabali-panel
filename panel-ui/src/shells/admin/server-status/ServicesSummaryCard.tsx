// ServicesSummaryCard — compact Service / Status / Action table for the
// Server Status top section. Replaces the larger ServicesGrid (which
// was Memory/Tasks/Uptime + a controls toggle); this version exposes
// Restart/Reload/Start/Stop inline behind a Popconfirm, off-by-default
// for destructive actions still warns operator before firing.
import { Button, Card, Popconfirm, Table, Tag, message } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  SyncOutlined,
} from "@icons";

import { apiClient } from "../../../apiClient";
import type { ServiceDetail } from "../../../hooks/useServerStatus";

interface Props {
  services: ServiceDetail[];
}

// reloadCapable lists units whose canonical "apply config without
// downtime" verb is reload, not restart. Anything else falls back to
// restart (which is correct for stateful services like mariadb).
const reloadCapable = new Set([
  "nginx.service",
  "pdns.service",
  "pdns-recursor.service",
]);

export function ServicesSummaryCard({ services }: Props) {
  const qc = useQueryClient();
  const ctl = useMutation({
    mutationFn: async ({ unit, action }: { unit: string; action: string }) => {
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
    <Card title="Services" size="small">
      <Table<ServiceDetail>
        rowKey="unit"
        size="small"
        dataSource={services}
        pagination={false}
        scroll={{ x: "max-content" }}
        showHeader={false}
        columns={[
          {
            title: "Service",
            dataIndex: "unit",
            render: (u: string) => prettyName(u),
          },
          {
            title: "Status",
            dataIndex: "active",
            width: 80,
            render: (s: string) => statusIcon(s),
          },
          {
            title: "Action",
            width: 110,
            align: "right" as const,
            render: (_: unknown, r: ServiceDetail) => {
              if (r.active === "inactive" || r.active === "failed") {
                return (
                  <Button
                    size="small"
                    type="text"
                    icon={<PlayCircleOutlined />}
                    onClick={() => ctl.mutate({ unit: r.unit, action: "start" })}
                  >
                    Start
                  </Button>
                );
              }
              const isReload = reloadCapable.has(r.unit);
              return (
                <Popconfirm
                  title={`${isReload ? "Reload" : "Restart"} ${prettyName(r.unit)}?`}
                  onConfirm={() =>
                    ctl.mutate({ unit: r.unit, action: isReload ? "reload" : "restart" })
                  }
                >
                  <Button
                    size="small"
                    type="text"
                    icon={isReload ? <ReloadOutlined /> : <SyncOutlined />}
                  >
                    {isReload ? "Reload" : "Restart"}
                  </Button>
                </Popconfirm>
              );
            },
          },
        ]}
      />
    </Card>
  );
}

function prettyName(unit: string): string {
  const base = unit.replace(/\.service$/, "");
  // Strip jabali- prefix for the panel-managed services to match the
  // operator's mental model ("Stalwart" not "jabali-stalwart").
  return base.replace(/^jabali-/, "");
}

function statusIcon(state: string) {
  switch (state) {
    case "active":
      return <Tag color="green" icon={<CheckCircleOutlined />} bordered={false} />;
    case "failed":
    case "inactive":
      return <Tag color="red" icon={<CloseCircleOutlined />} bordered={false} />;
    case "activating":
    case "deactivating":
      return <Tag color="orange" icon={<SyncOutlined spin />} bordered={false} />;
    default:
      return <Tag color="default">{state}</Tag>;
  }
}

