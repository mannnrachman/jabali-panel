// ProcessesCard — header counts + two side-by-side top-10 tables
// (CPU% and RSS) with a Kill icon-button per row. The agent owns the
// denylist; the UI just confirms before posting.
import { useState } from "react";
import { Button, Card, Modal, Space, Statistic, Table, Tooltip, message } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { CloseOutlined, ThunderboltOutlined } from "@icons";

import { apiClient } from "../../../apiClient";
import type { ProcessesSlice, ProcessTop } from "../../../hooks/useServerStatus";

interface Props {
  processes: ProcessesSlice | null;
}

export function ProcessesCard({ processes }: Props) {
  const [expanded, setExpanded] = useState(false);
  const [pendingKill, setPendingKill] = useState<{ pid: number; comm: string; force: boolean } | null>(null);
  const qc = useQueryClient();
  const p = processes;

  const killMutation = useMutation({
    mutationFn: async ({ pid, force }: { pid: number; force: boolean }) => {
      await apiClient.post(`/admin/processes/${pid}/kill`, { force });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "server-status"] });
      message.success("Signal sent");
    },
    onError: (e: unknown) => {
      message.error(e instanceof Error ? e.message : "Kill failed");
    },
  });

  const askKill = (row: ProcessTop, force: boolean) => {
    setPendingKill({ pid: row.pid, comm: row.comm, force });
  };

  const confirmKill = () => {
    if (!pendingKill) return;
    killMutation.mutate({ pid: pendingKill.pid, force: pendingKill.force });
    setPendingKill(null);
  };

  return (
    <>
      <Card
        title="Processes"
        size="small"
        extra={
          <Button size="small" onClick={() => setExpanded((v) => !v)}>
            {expanded ? "Hide top-10" : "Show top-10"}
          </Button>
        }
      >
        <Space size={24} wrap>
          <Statistic title="Total" value={p?.total ?? 0} />
          <Statistic title="Running" value={p?.running ?? 0} />
          <Statistic title="Sleeping" value={p?.sleeping ?? 0} />
          <Statistic
            title="Zombie"
            value={p?.zombie ?? 0}
            valueStyle={{ color: (p?.zombie ?? 0) > 0 ? "#cf1322" : undefined }}
          />
        </Space>
        {expanded && p ? (
          <div style={{ marginTop: 16, display: "grid", gridTemplateColumns: "1fr", gap: 16 }}>
            <ProcTable
              title="Top 10 by CPU"
              valueLabel="CPU"
              rows={p.top_by_cpu ?? []}
              valueRender={(r) => `${(r.cpu_percent ?? 0).toFixed(1)}%`}
              onKill={askKill}
            />
            <ProcTable
              title="Top 10 by RAM"
              valueLabel="RSS"
              rows={p.top_by_rss ?? []}
              valueRender={(r) => humanKB(r.rss_kb)}
              onKill={askKill}
            />
          </div>
        ) : null}
      </Card>
      <Modal
        open={!!pendingKill}
        title={pendingKill ? `${pendingKill.force ? "SIGKILL" : "SIGTERM"} pid ${pendingKill.pid} (${pendingKill.comm})?` : ""}
        okText={pendingKill?.force ? "SIGKILL" : "SIGTERM"}
        okButtonProps={{ danger: true }}
        onOk={confirmKill}
        onCancel={() => setPendingKill(null)}
      >
        {pendingKill?.force ? (
          <p>SIGKILL is unmaskable — the process gets no chance to flush state. Use SIGTERM first when in doubt.</p>
        ) : (
          <p>SIGTERM asks the process to clean up and exit. If it ignores the signal, retry with Force.</p>
        )}
      </Modal>
    </>
  );
}

interface ProcTableProps {
  title: string;
  valueLabel: string;
  rows: ProcessTop[];
  valueRender: (r: ProcessTop) => string;
  onKill: (r: ProcessTop, force: boolean) => void;
}

function ProcTable({ title, valueLabel, rows, valueRender, onKill }: ProcTableProps) {
  return (
    <div>
      <div style={{ marginBottom: 6, fontWeight: 500 }}>{title}</div>
      <Table<ProcessTop>
        rowKey="pid"
        size="small"
        dataSource={rows}
        pagination={false}
        scroll={{ x: "max-content" }}
        columns={[
          { title: "PID", dataIndex: "pid", width: 70 },
          { title: "Comm", dataIndex: "comm", ellipsis: true },
          { title: "User", dataIndex: "user", width: 90 },
          {
            title: valueLabel,
            width: 90,
            render: (_: unknown, r: ProcessTop) => valueRender(r),
          },
          {
            title: "",
            width: 70,
            align: "right" as const,
            render: (_: unknown, r: ProcessTop) => (
              <Space size={4}>
                <Tooltip title="SIGTERM (graceful)">
                  <Button
                    size="small"
                    type="text"
                    icon={<CloseOutlined />}
                    onClick={() => onKill(r, false)}
                    aria-label="SIGTERM"
                  />
                </Tooltip>
                <Tooltip title="SIGKILL (force)">
                  <Button
                    size="small"
                    type="text"
                    danger
                    icon={<ThunderboltOutlined />}
                    onClick={() => onKill(r, true)}
                    aria-label="SIGKILL"
                  />
                </Tooltip>
              </Space>
            ),
          },
        ]}
      />
    </div>
  );
}

function humanKB(kb: number): string {
  if (!kb) return "0";
  const units = ["KB", "MB", "GB"];
  let v = kb;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}
