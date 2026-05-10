// Admin Migration detail page — per-job stage timeline.
// Backed by GET /admin/migrations/:id which returns
// { job, stages: [{stage_name, state, started_at, ended_at,
//   bytes_processed, last_error}] }.
//
// Polls every 10s when the job is in a non-terminal state so the
// operator sees stages advance live while the cobra CLI runs.
import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Empty,
  Form,
  Input,
  message,
  Modal,
  Radio,
  Space,
  Spin,
  Steps,
  Tag,
  Typography,
} from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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

      <DriveCard jobId={job.id} jobState={job.state} jobSourceKind={job.source_kind} />

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
        <FailedCard jobId={job.id} onDestroyed={() => navigate("/jabali-admin/migrations")} />
      )}
    </Space>
  );
};

function FailedCard({ jobId, onDestroyed }: { jobId: string; onDestroyed: () => void }) {
  const destroy = useMutation({
    mutationFn: async () => {
      await apiClient.post(`/admin/migrations/${jobId}/destroy`);
    },
    onSuccess: () => {
      message.success("Job destroyed — create a new migration to retry.");
      onDestroyed();
    },
    onError: (e: unknown) => {
      const detail = (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail;
      message.error(detail ?? "Destroy failed");
    },
  });

  return (
    <Alert
      type="error"
      showIcon
      message="Migration failed"
      description={
        <Space direction="vertical" size="small" style={{ marginTop: 8 }}>
          <Typography.Text>
            Check the stage timeline above for the error detail.
            To retry, destroy this job and create a new migration from the list page.
          </Typography.Text>
          <Button
            danger
            size="small"
            loading={destroy.isPending}
            onClick={() => destroy.mutate()}
          >
            Destroy job and start over
          </Button>
        </Space>
      }
    />
  );
}

/**
 * DriveCard — three actions that drive a migration end-to-end:
 *   1. Upload secrets   → POST /admin/migrations/:id/secrets
 *                         (writes /etc/jabali-panel/migration-secrets/<id>.env)
 *   2. Pull source      → POST /admin/migrations/:id/pull-source
 *                         (transient unit jabali-migrate-pull-<id>.service)
 *   3. Run import       → POST /admin/migrations/:id/import
 *                         (transient unit jabali-migrate-import-<id>.service)
 *
 * Hidden once the job is in a terminal state — the agent endpoints
 * 409 anyway, but UI omitting the buttons is clearer than a flashing
 * error.
 */
function DriveCard({
  jobId,
  jobState,
  jobSourceKind,
}: {
  jobId: string;
  jobState: string;
  jobSourceKind: string;
}) {
  const queryClient = useQueryClient();
  const [secretsOpen, setSecretsOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [credKind, setCredKind] = useState<"password" | "private_key">(
    "password",
  );
  const [secretsForm] = Form.useForm();
  const [importForm] = Form.useForm();

  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ["admin-migrations", jobId] });

  const uploadSecrets = useMutation({
    mutationFn: async (vals: {
      ssh_password?: string;
      ssh_private_key?: string;
    }) => {
      const { data } = await apiClient.post(
        `/admin/migrations/${jobId}/secrets`,
        vals,
      );
      return data;
    },
    onSuccess: () => {
      message.success("Secrets uploaded.");
      setSecretsOpen(false);
      secretsForm.resetFields();
    },
    onError: (e: unknown) => {
      message.error(
        `Secrets upload failed: ${(e as Error)?.message ?? "unknown"}`,
      );
    },
  });

  const pullSource = useMutation({
    mutationFn: async () => {
      const { data } = await apiClient.post(
        `/admin/migrations/${jobId}/pull-source`,
        { ssh_user: "root" },
      );
      return data as { unit?: string };
    },
    onSuccess: (d) => {
      message.success(`Pull started: ${d?.unit ?? "(unit name unavailable)"}`);
      refresh();
    },
    onError: (e: unknown) => {
      message.error(
        `Pull failed to start: ${(e as Error)?.message ?? "unknown"}`,
      );
    },
  });

  const runImport = useMutation({
    mutationFn: async (vals: {
      target_user: string;
      target_email?: string;
      target_password?: string;
      target_package_id?: string;
    }) => {
      const { data } = await apiClient.post(
        `/admin/migrations/${jobId}/import`,
        vals,
      );
      return data as { unit?: string };
    },
    onSuccess: (d) => {
      message.success(
        `Import started: ${d?.unit ?? "(unit name unavailable)"}`,
      );
      setImportOpen(false);
      importForm.resetFields();
      refresh();
    },
    onError: (e: unknown) => {
      message.error(
        `Import failed to start: ${(e as Error)?.message ?? "unknown"}`,
      );
    },
  });

  // WHM pkgacct (offline) flow swaps the SSH-pull half for a tarball
  // upload. Detected by source_kind so the same DriveCard handles
  // both flows without a parent-side fork.
  const isOffline = jobSourceKind === "whm_pkgacct";

  // Tarball status — drives the "Upload tarball" → "Tarball staged"
  // UI swap. 5s poll while waiting for the upload to finish; once
  // present, polling stops (no new state to wait for).
  const tarballStatus = useQuery<{
    present: boolean;
    size_bytes?: number;
    mtime?: string;
  }>({
    queryKey: ["admin-migrations", jobId, "tarball"],
    enabled: isOffline && !isTerminal(jobState),
    refetchInterval: (q) =>
      q.state.data?.present ? false : 5_000,
    queryFn: async () => {
      const { data } = await apiClient.get(
        `/admin/migrations/${jobId}/tarball`,
      );
      return data;
    },
  });

  const uploadTarball = useMutation({
    mutationFn: async (file: File) => {
      const fd = new FormData();
      fd.append("file", file);
      const { data } = await apiClient.post(
        `/admin/migrations/${jobId}/tarball`,
        fd,
        {
          headers: { "Content-Type": "multipart/form-data" },
          // No timeout — multi-GB uploads need to run for as long as
          // the network sustains them.
          timeout: 0,
        },
      );
      return data as { path: string; size_bytes: number };
    },
    onSuccess: (d) => {
      message.success(
        `Tarball uploaded (${(d.size_bytes / (1024 * 1024)).toFixed(1)} MiB)`,
      );
      queryClient.invalidateQueries({
        queryKey: ["admin-migrations", jobId, "tarball"],
      });
    },
    onError: (e: unknown) => {
      message.error(
        `Tarball upload failed: ${(e as Error)?.message ?? "unknown"}`,
      );
    },
  });

  if (isTerminal(jobState)) {
    return null;
  }

  return (
    <Card size="small" title="Drive migration">
      <Space wrap>
        {isOffline ? (
          <>
            <input
              type="file"
              accept=".tar,.tar.gz,.tgz,.tar.bz2,.tar.xz"
              style={{ display: "none" }}
              id={`tarball-input-${jobId}`}
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) uploadTarball.mutate(f);
                e.target.value = "";
              }}
            />
            <Button
              type={tarballStatus.data?.present ? "default" : "primary"}
              loading={uploadTarball.isPending}
              onClick={() =>
                document
                  .getElementById(`tarball-input-${jobId}`)
                  ?.click()
              }
            >
              {tarballStatus.data?.present
                ? "Replace tarball…"
                : "1. Upload tarball…"}
            </Button>
            {tarballStatus.data?.present && (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                Staged:{" "}
                {((tarballStatus.data.size_bytes ?? 0) / (1024 * 1024)).toFixed(
                  1,
                )}{" "}
                MiB
              </Typography.Text>
            )}
          </>
        ) : (
          <>
            <Button onClick={() => setSecretsOpen(true)}>
              1. Upload secrets…
            </Button>
            <Button
              type="primary"
              loading={pullSource.isPending}
              onClick={() => pullSource.mutate()}
            >
              2. Pull source
            </Button>
          </>
        )}
        <Button
          type="primary"
          loading={runImport.isPending}
          disabled={isOffline && !tarballStatus.data?.present}
          onClick={() => setImportOpen(true)}
        >
          {isOffline ? "2. Run import…" : "3. Run import…"}
        </Button>
      </Space>
      <Typography.Paragraph
        type="secondary"
        style={{ marginTop: 12, marginBottom: 0, fontSize: 12 }}
      >
        {isOffline
          ? "Tarball streams to /var/lib/jabali-migrations/<id>/source.tar.gz on the panel host. Run import once the upload completes — it survives panel restart via a transient systemd unit. Stage rows update on the 10s poll."
          : "Each action runs detached as a transient systemd unit so it survives a panel restart. Stage rows update on the 10s poll."}
      </Typography.Paragraph>

      <Modal
        title="Upload SSH secrets"
        open={secretsOpen}
        onCancel={() => setSecretsOpen(false)}
        onOk={() => secretsForm.submit()}
        confirmLoading={uploadSecrets.isPending}
        okText="Upload"
      >
        <Form
          form={secretsForm}
          layout="vertical"
          onFinish={(vals: { secret: string }) => {
            if (credKind === "password") {
              uploadSecrets.mutate({ ssh_password: vals.secret });
            } else {
              uploadSecrets.mutate({ ssh_private_key: vals.secret });
            }
          }}
        >
          <Form.Item label="Credential type">
            <Radio.Group
              value={credKind}
              onChange={(e) => setCredKind(e.target.value)}
            >
              <Radio.Button value="password">Password</Radio.Button>
              <Radio.Button value="private_key">Private key</Radio.Button>
            </Radio.Group>
          </Form.Item>
          {credKind === "password" ? (
            <Form.Item
              name="secret"
              label="SSH password"
              rules={[{ required: true, message: "required" }]}
            >
              <Input.Password autoComplete="new-password" />
            </Form.Item>
          ) : (
            <Form.Item
              name="secret"
              label="Private key (PEM)"
              rules={[{ required: true, message: "required" }]}
            >
              <Input.TextArea
                rows={8}
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              />
            </Form.Item>
          )}
        </Form>
      </Modal>

      <Modal
        title="Run import"
        open={importOpen}
        onCancel={() => setImportOpen(false)}
        onOk={() => importForm.submit()}
        confirmLoading={runImport.isPending}
        okText="Run import"
      >
        <Form
          form={importForm}
          layout="vertical"
          onFinish={(vals) => runImport.mutate(vals)}
        >
          <Form.Item
            name="target_user"
            label="Destination username"
            rules={[
              { required: true, message: "required" },
              {
                pattern: /^[a-z][a-z0-9-]{0,31}$/,
                message: "1-32 chars, lowercase, alnum + hyphen",
              },
            ]}
          >
            <Input placeholder="e.g. acme" />
          </Form.Item>
          <Form.Item
            name="target_email"
            label="Email (only if auto-creating)"
          >
            <Input placeholder="owner@example.com" />
          </Form.Item>
          <Form.Item
            name="target_password"
            label="Password (only if auto-creating, ≥10 chars)"
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="target_package_id"
            label="Package ID (only if auto-creating)"
          >
            <Input placeholder="ULID — leave blank for default package" />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Auto-create requires email + password. Pre-existing target
            user matched by username works without these.
          </Typography.Text>
        </Form>
      </Modal>
    </Card>
  );
}

export default AdminMigrationDetailPage;
