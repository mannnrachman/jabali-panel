// ServicesSummaryCard — compact Service / Status / Action table for the
// Server Status top section. Action column is a row of icon-only
// Buttons each wrapped in a Tooltip explaining the verb. Self-destruct
// trio (jabali-panel, jabali-agent, mariadb) hides Stop+Disable —
// those requests are 403'd at the API anyway. Destructive verbs
// (stop/disable/restart) show a confirm Modal first.
import { useState } from "react";
import {
  Card,
  Modal,
  Space,
  Table,
  Tag,
  Tooltip,
  message,
} from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import {
  CheckCircleOutlined,
  CloseOutlined,
  PauseCircleOutlined,
  PlayCircleOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  SettingOutlined,
  SyncOutlined,
} from "@icons";
import { RowActionButton } from "../../../components/RowActionButton";

import { apiClient } from "../../../apiClient";
import type { ServiceDetail } from "../../../hooks/useServerStatus";

interface Props {
  services: ServiceDetail[];
}

const reloadCapable = new Set([
  "nginx.service",
  "pdns.service",
  "pdns-recursor.service",
]);

const selfDestructUnits = new Set([
  "jabali-panel.service",
  "jabali-agent.service",
  "mariadb.service",
]);

type Action = "restart" | "reload" | "start" | "stop" | "enable" | "disable";

const destructiveActions = new Set<Action>(["stop", "disable", "restart"]);

export function ServicesSummaryCard({ services }: Props) {
  const qc = useQueryClient();
  const [pending, setPending] = useState<{ unit: string; action: Action } | null>(null);

  const ctl = useMutation({
    mutationFn: async ({ unit, action }: { unit: string; action: Action }) => {
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

  const runAction = (unit: string, action: Action) => {
    if (destructiveActions.has(action)) {
      setPending({ unit, action });
      return;
    }
    ctl.mutate({ unit, action });
  };

  const confirmPending = () => {
    if (!pending) return;
    ctl.mutate(pending);
    setPending(null);
  };

  return (
    <>
      <Card title={<><SettingOutlined /> Services</>} size="small">
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
              width: 120,
              render: (s: string) => statusIcon(s),
            },
            {
              title: "Actions",
              width: 200,
              align: "right" as const,
              render: (_: unknown, r: ServiceDetail) => (
                <ServiceActions service={r} onAction={runAction} />
              ),
            },
          ]}
        />
      </Card>
      <Modal
        open={!!pending}
        title={pending ? `${capitalize(pending.action)} ${prettyName(pending.unit)}?` : ""}
        okText={pending ? capitalize(pending.action) : "OK"}
        okButtonProps={{ danger: true }}
        onOk={confirmPending}
        onCancel={() => setPending(null)}
      >
        {pending?.action === "stop" && (
          <p>This will halt the unit. Dependent panel features may stop working until you start it again.</p>
        )}
        {pending?.action === "disable" && (
          <p>This will prevent the unit from starting at boot. Combine with Stop if you also want it down right now.</p>
        )}
        {pending?.action === "restart" && (
          <p>Restart causes a brief drop in service. Continue?</p>
        )}
      </Modal>
    </>
  );
}

interface ServiceActionsProps {
  service: ServiceDetail;
  onAction: (unit: string, action: Action) => void;
}

function ServiceActions({ service, onAction }: ServiceActionsProps) {
  const isDown = service.active === "inactive" || service.active === "failed";
  const isReload = reloadCapable.has(service.unit);
  const isSelfDestruct = selfDestructUnits.has(service.unit);
  const isEnabled =
    service.unit_file_state === "enabled" ||
    service.unit_file_state === "enabled-runtime" ||
    service.unit_file_state === "static" ||
    service.unit_file_state === "alias";

  return (
    <Space size={2}>
      {isDown ? (
        <Tooltip title="Start">
          <RowActionButton
            size="small"
            icon={<PlayCircleOutlined />}
            onClick={() => onAction(service.unit, "start")}
            aria-label="Start"
          >
            Start
          </RowActionButton>
        </Tooltip>
      ) : (
        <>
          <Tooltip title="Restart">
            <RowActionButton
              size="small"
              icon={<SyncOutlined />}
              onClick={() => onAction(service.unit, "restart")}
              aria-label="Restart"
            >
              Restart
            </RowActionButton>
          </Tooltip>
          {isReload && (
            <Tooltip title="Reload">
              <RowActionButton
                size="small"
                icon={<ReloadOutlined />}
                onClick={() => onAction(service.unit, "reload")}
                aria-label="Reload"
              >
                Reload
              </RowActionButton>
            </Tooltip>
          )}
          {!isSelfDestruct && (
            <Tooltip title="Stop">
              <RowActionButton
                size="small"
                danger
                icon={<PauseCircleOutlined />}
                onClick={() => onAction(service.unit, "stop")}
                aria-label="Stop"
              >
                Stop
              </RowActionButton>
            </Tooltip>
          )}
        </>
      )}
      {isEnabled
        ? !isSelfDestruct && (
            <Tooltip title="Disable at boot">
              <RowActionButton
                size="small"
                danger
                icon={<PoweroffOutlined />}
                onClick={() => onAction(service.unit, "disable")}
                aria-label="Disable at boot"
              >
                Disable
              </RowActionButton>
            </Tooltip>
          )
        : (
          <Tooltip title="Enable at boot">
            <RowActionButton
              size="small"
              icon={<PoweroffOutlined />}
              onClick={() => onAction(service.unit, "enable")}
              aria-label="Enable at boot"
            >
              Enable
            </RowActionButton>
          </Tooltip>
        )}
    </Space>
  );
}

function prettyName(unit: string): string {
  const base = unit.replace(/\.service$/, "");
  return base.replace(/^jabali-/, "");
}

function statusIcon(state: string) {
  switch (state) {
    case "active":
      return <Tag color="green" icon={<CheckCircleOutlined />} bordered={false}>active</Tag>;
    case "failed":
      return <Tag color="red" icon={<CloseOutlined />} bordered={false}>failed</Tag>;
    case "inactive":
      return <Tag color="red" icon={<CloseOutlined />} bordered={false}>inactive</Tag>;
    case "activating":
      return <Tag color="orange" icon={<SyncOutlined spin />} bordered={false}>activating</Tag>;
    case "deactivating":
      return <Tag color="orange" icon={<SyncOutlined spin />} bordered={false}>deactivating</Tag>;
    default:
      return <Tag color="default">{state}</Tag>;
  }
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
