// ServicesSummaryCard — compact Service / Status / Action table for the
// Server Status top section. Action column is a Dropdown.Button: the
// primary verb is the "obvious next step" (Start when down, Restart/
// Reload when up); destructive verbs (stop/disable) are tucked into
// the menu behind a Popconfirm. Self-destruct trio (jabali-panel,
// jabali-agent, mariadb) hides Stop+Disable items entirely — those
// requests are 403'd at the API anyway.
import { useState } from "react";
import {
  Card,
  Dropdown,
  type MenuProps,
  Modal,
  Table,
  Tag,
  message,
} from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import {
  CheckCircleOutlined,
  CloseOutlined,
  DownOutlined,
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

// selfDestructUnits mirrors the API-side allow-list. Stop+Disable on
// these would brick the management plane mid-request; the API rejects
// them with 403 and the UI hides the menu items entirely.
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
              width: 160,
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
          <p>
            This will halt the unit. Dependent panel features may stop
            working until you start it again.
          </p>
        )}
        {pending?.action === "disable" && (
          <p>
            This will prevent the unit from starting at boot. Combine with
            Stop if you also want it down right now.
          </p>
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

  // Primary verb = the obvious next step. Down → Start. Up → Restart
  // (or Reload for nginx/pdns). The dropdown carries the rest.
  const primary: Action = isDown ? "start" : isReload ? "reload" : "restart";
  const primaryLabel = isDown ? "Start" : isReload ? "Reload" : "Restart";
  const primaryIcon = isDown
    ? <PlayCircleOutlined />
    : isReload
      ? <ReloadOutlined />
      : <SyncOutlined />;

  const items: MenuProps["items"] = [];
  // Always offer the non-primary up-actions when up.
  if (!isDown) {
    if (primary !== "restart") items.push({ key: "restart", label: "Restart" });
    if (primary !== "reload" && isReload) items.push({ key: "reload", label: "Reload" });
    if (!isSelfDestruct) {
      items.push({ key: "stop", label: "Stop", danger: true });
    }
  }
  // Enable/disable show in both up and down states.
  items.push({ type: "divider" });
  if (isEnabled) {
    if (!isSelfDestruct) {
      items.push({ key: "disable", label: "Disable at boot", danger: true });
    } else {
      items.push({ key: "enable", label: "Already enabled", disabled: true });
    }
  } else {
    items.push({ key: "enable", label: "Enable at boot" });
  }

  const menu: MenuProps = {
    items,
    onClick: ({ key }) => onAction(service.unit, key as Action),
  };

  return (
    <Dropdown.Button
      size="small"
      type="text"
      icon={<DownOutlined />}
      menu={menu}
      onClick={() => onAction(service.unit, primary)}
    >
      {primaryIcon}
      {primaryLabel}
    </Dropdown.Button>
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
      return <Tag color="red" icon={<CloseOutlined />} bordered={false} />;
    case "activating":
    case "deactivating":
      return <Tag color="orange" icon={<SyncOutlined spin />} bordered={false} />;
    default:
      return <Tag color="default">{state}</Tag>;
  }
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
