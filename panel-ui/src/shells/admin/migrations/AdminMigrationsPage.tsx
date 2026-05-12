// Admin Migrations page — read-only list view (M35 Step 8 partial).
// Mutation flows (start a new migration, resume, cancel a running
// job) land alongside JMAP-push + DA per-area-builders since those
// gate when the operator can self-service end-to-end. Today the
// page surfaces:
//   - paginated table of migration_jobs
//   - per-row state tag (color-coded) + source kind badge
//   - per-row Cancel button (calls DELETE /admin/migrations/:id)
//   - per-row 'View' link to a future detail page (queued)
//
// Backed by panel-api/internal/api/admin_migrations.go (commit
// 5981541a).
import { useState } from "react";
import {
  Alert,
  Button,
  Card,
  Empty,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";

import { apiClient } from "../../../apiClient";
import { RowActionButton } from "../../../components/RowActionButton";
import { DeleteOutlined, PlusOutlined, SwapOutlined } from "@icons";
import { BulkWhmDrawer } from "./BulkWhmDrawer";
import { CreateMigrationDrawer } from "./CreateMigrationDrawer";
import { CreateMigrationWizard } from "./CreateMigrationWizard";

type MigrationJob = {
  id: string;
  batch_id: string | null;
  source_kind: string;
  source_host: string;
  source_user: string;
  target_user_id: string | null;
  state: string;
  started_at: string;
  ended_at: string | null;
  last_error: string | null;
  created_at: string;
  updated_at: string;
};

type MigrationJobListResponse = {
  data: MigrationJob[];
  total: number;
  page: number;
  page_size: number;
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

const SOURCE_BADGE: Record<string, { color: string; label: string }> = {
  cpanel: { color: "purple", label: "cPanel" },
  whm_pkgacct: { color: "purple", label: "WHM (pkgacct)" },
  directadmin: { color: "cyan", label: "DirectAdmin" },
  hestiacp: { color: "magenta", label: "HestiaCP" },
};

export const AdminMigrationsPage = () => {
  const qc = useQueryClient();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [bulkOpen, setBulkOpen] = useState(false);
  const [wizardOpen, setWizardOpen] = useState(false);

  const list = useQuery<MigrationJobListResponse>({
    queryKey: ["admin-migrations"],
    queryFn: async () => {
      const { data } = await apiClient.get<MigrationJobListResponse>(
        "/admin/migrations?page_size=200",
      );
      return data;
    },
    refetchInterval: 30_000, // poll while a job is in-flight
  });

  const cancelBatch = useMutation<void, unknown, { batchId: string }>({
    mutationFn: async ({ batchId }) => {
      await apiClient.delete(`/admin/migrations/batches/${batchId}`);
    },
    onSuccess: async () => {
      message.success("Batch cancelled");
      await qc.invalidateQueries({ queryKey: ["admin-migrations"] });
    },
    onError: (err) => {
      const detail =
        (err as { response?: { data?: { error?: string; detail?: string } } })
          ?.response?.data?.detail;
      message.error(detail ?? "Batch cancel failed");
    },
  });

  const cancel = useMutation<void, unknown, { id: string }>({
    mutationFn: async ({ id }) => {
      await apiClient.delete(`/admin/migrations/${id}`);
    },
    onSuccess: async () => {
      message.success("Cancelled");
      await qc.invalidateQueries({ queryKey: ["admin-migrations"] });
    },
    onError: (err) => {
      const detail =
        (err as { response?: { data?: { error?: string; detail?: string } } })
          ?.response?.data?.detail;
      message.error(detail ?? "Cancel failed");
    },
  });

  const destroy = useMutation<void, unknown, { id: string }>({
    mutationFn: async ({ id }) => {
      await apiClient.post(`/admin/migrations/${id}/destroy`);
    },
    onSuccess: async () => {
      message.success("Destroyed");
      await qc.invalidateQueries({ queryKey: ["admin-migrations"] });
    },
    onError: (err) => {
      const detail =
        (err as { response?: { data?: { error?: string; detail?: string } } })
          ?.response?.data?.detail;
      message.error(detail ?? "Destroy failed");
    },
  });

  const rows = list.data?.data ?? [];

  return (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="Account migration importer (M35)"
        description={
          <Typography.Paragraph style={{ marginBottom: 0 }}>
            Read-only view of the migration_jobs table. Start new migrations
            via the <Typography.Text code>jabali migrate import</Typography.Text>{" "}
            CLI today; admin SPA mutation flows land once the JMAP-push and
            per-area-builder follow-ups stabilise. See{" "}
            <Typography.Text code>plans/m35-migration-importers-runbook.md</Typography.Text>{" "}
            for the per-account workflow.
          </Typography.Paragraph>
        }
      />

      <Card
        size="small"
        title="Migration jobs"
        extra={
          <Space wrap>
            <Button onClick={() => setBulkOpen(true)}>Bulk WHM (paste)</Button>
            <Button onClick={() => setWizardOpen(true)}>Wizard</Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setDrawerOpen(true)}
            >
              New migration
            </Button>
          </Space>
        }
      >
        <Table<MigrationJob>
          dataSource={rows}
          rowKey="id"
          loading={list.isLoading}
          pagination={{ pageSize: 50, hideOnSinglePage: true }}
          scroll={{ x: "max-content" }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="No migrations yet"
              />
            ),
          }}
        >
          <Table.Column<MigrationJob>
            title="Source"
            dataIndex="source_kind"
            render={(k: string) => {
              const b = SOURCE_BADGE[k] ?? { color: "default", label: k };
              return <Tag color={b.color}>{b.label}</Tag>;
            }}
          />
          <Table.Column<MigrationJob>
            title="Source host"
            dataIndex="source_host"
            render={(s: string) => (
              <Typography.Text code style={{ fontSize: 12 }}>
                {s}
              </Typography.Text>
            )}
          />
          <Table.Column<MigrationJob>
            title="Batch"
            dataIndex="batch_id"
            render={(b: string | null) =>
              b ? (
                <Popconfirm
                  title={`Cancel entire batch ${b.slice(-6)}?`}
                  description="Cancels every non-terminal job sharing this batch_id."
                  okText="Cancel batch"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => cancelBatch.mutate({ batchId: b })}
                >
                  <Tag
                    color="purple"
                    style={{ fontFamily: "monospace", fontSize: 11, cursor: "pointer" }}
                  >
                    {b.slice(-6)}
                  </Tag>
                </Popconfirm>
              ) : (
                <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                  —
                </Typography.Text>
              )
            }
          />
          <Table.Column<MigrationJob>
            title="Source user"
            dataIndex="source_user"
            render={(s: string) => (
              <Typography.Text code style={{ fontSize: 12 }}>
                {s}
              </Typography.Text>
            )}
          />
          <Table.Column<MigrationJob>
            title="State"
            dataIndex="state"
            render={(s: string) => {
              const t = STATE_TAG[s] ?? { color: "default", label: s };
              return <Tag color={t.color}>{t.label}</Tag>;
            }}
          />
          <Table.Column<MigrationJob>
            title="Started"
            dataIndex="started_at"
            render={(s: string) => new Date(s).toLocaleString()}
          />
          <Table.Column<MigrationJob>
            title="Ended"
            dataIndex="ended_at"
            render={(s: string | null) =>
              s ? new Date(s).toLocaleString() : "—"
            }
          />
          <Table.Column<MigrationJob>
            title=""
            width={200}
            render={(_, r) => {
              const isDraft = r.state === "draft";
              const terminal =
                r.state === "done" ||
                r.state === "failed" ||
                r.state === "cancelled";
              return (
                <Space size="small">
                  <Link to={`/jabali-admin/migrations/${r.id}`}>
                    <RowActionButton icon={<SwapOutlined />} color="default">
                      View
                    </RowActionButton>
                  </Link>
                  {isDraft && (
                    <Popconfirm
                      title={`Discard draft ${r.source_user}?`}
                      description="Hard-deletes the draft row. No secrets or extracted files have been written yet."
                      onConfirm={() => destroy.mutate({ id: r.id })}
                      okText="Discard"
                      okButtonProps={{ danger: true }}
                    >
                      <RowActionButton
                        danger
                        icon={<DeleteOutlined />}
                        color="default"
                      >
                        Discard
                      </RowActionButton>
                    </Popconfirm>
                  )}
                  {!isDraft && !terminal && (
                    <Popconfirm
                      title={`Cancel migration ${r.source_user}?`}
                      description="Stamps the DB row as cancelled. Does NOT kill an in-flight CLI process — Ctrl-C the cobra cmd separately."
                      onConfirm={() => cancel.mutate({ id: r.id })}
                      okText="Cancel job"
                      okButtonProps={{ danger: true }}
                    >
                      <RowActionButton
                        danger
                        icon={<DeleteOutlined />}
                        color="default"
                      >
                        Cancel
                      </RowActionButton>
                    </Popconfirm>
                  )}
                  {terminal && (
                    <Popconfirm
                      title={`Destroy migration ${r.source_user}?`}
                      description="Removes the DB row, secrets file, and /var/lib/jabali-migrations/<id>/ extracted dir. Operator-irreversible."
                      onConfirm={() => destroy.mutate({ id: r.id })}
                      okText="Destroy"
                      okButtonProps={{ danger: true }}
                    >
                      <RowActionButton
                        danger
                        icon={<DeleteOutlined />}
                        color="default"
                      >
                        Destroy
                      </RowActionButton>
                    </Popconfirm>
                  )}
                </Space>
              );
            }}
          />
        </Table>
      </Card>

      <Card size="small" title="Per-source-kind support">
        <Space direction="vertical" style={{ width: "100%" }}>
          <Typography.Text>
            <Tag color="green">cPanel</Tag> SSH discovery + pkgacct + full
            restore (5 area writers + mailbox stub)
          </Typography.Text>
          <Typography.Text>
            <Tag color="green">WHM (pkgacct)</Tag> Operator-uploaded tarball;
            reuses cPanel restore code-path
          </Typography.Text>
          <Typography.Text>
            <Tag color="green">DirectAdmin</Tag> Live SSH discover +
            system_backup_user tarball pull; restore via cpanel writers
          </Typography.Text>
          <Typography.Text>
            <Tag color="orange">HestiaCP</Tag> Discoverer scaffold only
            (DocRoots adapter pending M35.4)
          </Typography.Text>
          <Typography.Text>
            <Tag color="default">IMAP-only</Tag> Not yet wired
          </Typography.Text>
        </Space>
      </Card>
      <CreateMigrationDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
      />
      <BulkWhmDrawer
        open={bulkOpen}
        onClose={() => setBulkOpen(false)}
        onCreated={() => list.refetch()}
      />
      <CreateMigrationWizard
        open={wizardOpen}
        onClose={() => setWizardOpen(false)}
        onCreated={() => list.refetch()}
      />
    </Space>
  );
};

export default AdminMigrationsPage;
