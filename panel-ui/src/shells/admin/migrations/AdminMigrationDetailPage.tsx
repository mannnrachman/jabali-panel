// Admin Migration detail page — per-job stage timeline.
// Backed by GET /admin/migrations/:id which returns
// { job, stages: [{stage_name, state, started_at, ended_at,
//   bytes_processed, last_error}] }.
//
// Polls every 10s when the job is in a non-terminal state so the
// operator sees stages advance live while the cobra CLI runs.
import { useEffect, useMemo } from "react";
import {
  Alert,
  Card,
  Descriptions,
  Empty,
  Space,
  Spin,
  Steps,
  Tag,
  Typography,
} from "antd";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router";

import { apiClient } from "../../../apiClient";

type MigrationJob = {
  id: string;
  source_kind: string;
  source_host: string;
  source_user: string;
  target_user_id: string | null;
  state: string;
  started_at: string;
  ended_at: string | null;
  manifest_json: string | null;
  last_error: string | null;
  created_at: string;
  updated_at: string;
};

type MigrationStage = {
  id: string;
  job_id: string;
  stage_name: string;
  state: string;
  started_at: string | null;
  ended_at: string | null;
  bytes_processed: number;
  last_error: string | null;
  created_at: string;
  updated_at: string;
};

type DetailResponse = {
  job: MigrationJob;
  stages: MigrationStage[];
};

const STATE_TAG: Record<string, { color: string; label: string }> = {
  pending: { color: "default", label: "PENDING" },
  analyzing: { color: "blue", label: "ANALYZING" },
  fix_perms: { color: "blue", label: "FIX-PERMS" },
  validating: { color: "blue", label: "VALIDATING" },
  restoring: { color: "geekblue", label: "RESTORING" },
  done: { color: "green", label: "DONE" },
  failed: { color: "red", label: "FAILED" },
  cancelled: { color: "default", label: "CANCELLED" },
};

const STAGE_ORDER = ["analyze", "fix_perms", "validate", "restore"];

const STAGE_LABEL: Record<string, string> = {
  analyze: "Analyze",
  fix_perms: "Fix permissions",
  validate: "Validate",
  restore: "Restore",
};

function isTerminal(state: string): boolean {
  return state === "done" || state === "failed" || state === "cancelled";
}

export const AdminMigrationDetailPage = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const detail = useQuery<DetailResponse>({
    queryKey: ["admin-migrations", id],
    queryFn: async () => {
      const { data } = await apiClient.get<DetailResponse>(
        `/admin/migrations/${id}`,
      );
      return data;
    },
    enabled: !!id,
    refetchInterval: (q) => {
      const data = q.state.data as DetailResponse | undefined;
      // Stop polling once terminal — saves a tick / sec on a busy
      // operator's open page.
      if (!data) return 5_000;
      return isTerminal(data.job.state) ? false : 10_000;
    },
  });

  // Build the Steps progress prop from the stage rows. Stages
  // table keeps insertion order; when a stage hasn't been created
  // yet we render it as 'wait' (greyed) so the operator sees the
  // full pipeline even before the runner reaches each stage.
  const stagesByName = useMemo(() => {
    const m = new Map<string, MigrationStage>();
    for (const s of detail.data?.stages ?? []) {
      m.set(s.stage_name, s);
    }
    return m;
  }, [detail.data?.stages]);

  useEffect(() => {
    // Soft-refetch when the URL :id changes so navigating directly
    // from the list-page row doesn't show a stale row briefly.
    if (id) detail.refetch();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  if (!id) {
    return <Empty description="No migration job selected" />;
  }
  if (detail.isLoading || !detail.data) {
    return (
      <div style={{ textAlign: "center", padding: 48 }}>
        <Spin />
      </div>
    );
  }
  const { job, stages } = detail.data;

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Card>
        <Space style={{ marginBottom: 16 }}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            Migration {job.source_user}@{job.source_host}
          </Typography.Title>
          <Tag color={STATE_TAG[job.state]?.color ?? "default"}>
            {STATE_TAG[job.state]?.label ?? job.state}
          </Tag>
          <Typography.Link onClick={() => navigate("/jabali-admin/migrations")}>
            ← back to list
          </Typography.Link>
        </Space>

        <Descriptions size="small" column={2} bordered>
          <Descriptions.Item label="Job ID">
            <Typography.Text code>{job.id}</Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="Source kind">
            {job.source_kind}
          </Descriptions.Item>
          <Descriptions.Item label="Started">
            {new Date(job.started_at).toLocaleString()}
          </Descriptions.Item>
          <Descriptions.Item label="Ended">
            {job.ended_at ? new Date(job.ended_at).toLocaleString() : "—"}
          </Descriptions.Item>
          <Descriptions.Item label="Target user ID">
            {job.target_user_id ? (
              <Typography.Text code>{job.target_user_id}</Typography.Text>
            ) : (
              "—"
            )}
          </Descriptions.Item>
          <Descriptions.Item label="Last error">
            {job.last_error ?? "—"}
          </Descriptions.Item>
        </Descriptions>
      </Card>

      <Card size="small" title="Pipeline">
        <Steps
          direction="horizontal"
          responsive={false}
          current={STAGE_ORDER.findIndex(
            (n) => stagesByName.get(n)?.state === "running",
          )}
          items={STAGE_ORDER.map((name) => {
            const row = stagesByName.get(name);
            let status: "wait" | "process" | "finish" | "error" = "wait";
            if (row) {
              if (row.state === "done") status = "finish";
              else if (row.state === "running") status = "process";
              else if (row.state === "failed") status = "error";
            }
            return {
              title: STAGE_LABEL[name] ?? name,
              status,
              description: row
                ? `${row.state}${row.bytes_processed ? ` · ${row.bytes_processed} B` : ""}`
                : "pending",
            };
          })}
        />
      </Card>

      <Card size="small" title="Stage timeline">
        {stages.length === 0 ? (
          <Empty description="No stage rows yet" />
        ) : (
          <Space direction="vertical" style={{ width: "100%" }}>
            {stages.map((s) => (
              <Card key={s.id} size="small" type="inner">
                <Space direction="vertical" style={{ width: "100%" }}>
                  <Space>
                    <Typography.Text strong>
                      {STAGE_LABEL[s.stage_name] ?? s.stage_name}
                    </Typography.Text>
                    <Tag
                      color={
                        s.state === "done"
                          ? "green"
                          : s.state === "running"
                            ? "blue"
                            : s.state === "failed"
                              ? "red"
                              : "default"
                      }
                    >
                      {s.state.toUpperCase()}
                    </Tag>
                  </Space>
                  <Descriptions size="small" column={2}>
                    <Descriptions.Item label="Started">
                      {s.started_at
                        ? new Date(s.started_at).toLocaleString()
                        : "—"}
                    </Descriptions.Item>
                    <Descriptions.Item label="Ended">
                      {s.ended_at ? new Date(s.ended_at).toLocaleString() : "—"}
                    </Descriptions.Item>
                    <Descriptions.Item label="Bytes processed">
                      {s.bytes_processed.toLocaleString()}
                    </Descriptions.Item>
                    <Descriptions.Item label="Error">
                      {s.last_error ?? "—"}
                    </Descriptions.Item>
                  </Descriptions>
                </Space>
              </Card>
            ))}
          </Space>
        )}
      </Card>

      {job.manifest_json && (
        <Card
          size="small"
          title="Manifest / warnings (raw)"
          extra={
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              raw manifest_json from migration_jobs row
            </Typography.Text>
          }
        >
          <Typography.Paragraph
            style={{
              fontFamily: "monospace",
              fontSize: 12,
              whiteSpace: "pre-wrap",
              maxHeight: 320,
              overflowY: "auto",
            }}
          >
            {job.manifest_json}
          </Typography.Paragraph>
        </Card>
      )}

      {job.state === "failed" && (
        <Alert
          type="error"
          showIcon
          message="Migration failed"
          description={
            <Typography.Paragraph style={{ marginBottom: 0 }}>
              Re-run via{" "}
              <Typography.Text code>
                jabali migrate import --job-id {job.id} --target-user{" "}
                &lt;username&gt;
              </Typography.Text>{" "}
              to resume from the first failed stage. See{" "}
              <Typography.Text code>
                plans/m35-migration-importers-runbook.md
              </Typography.Text>{" "}
              §5 for recovery scenarios.
            </Typography.Paragraph>
          }
        />
      )}
    </Space>
  );
};

export default AdminMigrationDetailPage;
