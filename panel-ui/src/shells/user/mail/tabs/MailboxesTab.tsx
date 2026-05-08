// MailboxesTab — cross-domain mailbox list + actions.
//
// Lists mailboxes from all user domains in a single table. Extracted from
// the original UserMailboxesPage. Merges results from per-domain list queries
// client-side and provides password rotation, SSO mint, and delete actions.
import { useMemo, useState } from "react";
import {
  Button,
  Empty,
  Popconfirm,
  Progress,
  Skeleton,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import {
  DeleteOutlined,
  KeyOutlined,
  MailOutlined,
} from "@icons";
import { RowActionButton } from "../../../../components/RowActionButton";
import { useQueries } from "@tanstack/react-query";

import { apiClient } from "../../../../apiClient";
import {
  useDeleteMailbox,
  useMintMailboxSSO,
  useRotateMailboxPassword,
  type Mailbox,
} from "../../../../hooks/useMailboxes";
import { useListQuery } from "../../../../hooks/useQueries";
import type { Domain } from "../../domains/UserDomainList";
import { DatabaseUserPasswordModal } from "../../../../components/DatabaseUserPasswordModal";

type MailboxRow = Mailbox & { domain_name: string };

function formatBytes(n: number): string {
  if (n === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.floor(Math.log(n) / Math.log(1024));
  const v = n / Math.pow(1024, i);
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export const MailboxesTab = () => {
  const { items: domains, isLoading: loadingDomains } = useListQuery<Domain>({
    resource: "domains",
    params: { page: 1, pageSize: 200, sort: "name", order: "asc" },
  });

  const emailEnabledDomains = useMemo(
    () => domains.filter((d) => d.email_enabled),
    [domains],
  );

  const mailboxResults = useQueries({
    queries: emailEnabledDomains.map((d) => ({
      queryKey: ["list", "mailboxes", d.id, { page: 1, pageSize: 200 }],
      queryFn: async () => {
        const { data } = await apiClient.get<{ data: Mailbox[]; total: number }>(
          `/domains/${d.id}/mailboxes?page=1&page_size=200&sort=local_part&order=asc`,
        );
        return { items: data.data ?? [], domain: d };
      },
    })),
  });

  const rows: MailboxRow[] = useMemo(() => {
    const out: MailboxRow[] = [];
    for (const r of mailboxResults) {
      if (!r.data) continue;
      for (const mb of r.data.items) {
        out.push({ ...mb, domain_name: r.data.domain.name });
      }
    }
    return out;
  }, [mailboxResults]);

  const [passwordModal, setPasswordModal] = useState<{
    email: string;
    password: string;
    title: string;
  } | null>(null);
  const [rotatingId, setRotatingId] = useState<string | null>(null);

  const deleteMutation = useDeleteMailbox();
  const rotateMutation = useRotateMailboxPassword();
  const ssoMutation = useMintMailboxSSO();

  const rotate = async (row: MailboxRow) => {
    setRotatingId(row.id);
    try {
      const resp = await rotateMutation.mutateAsync({ id: row.id });
      if (resp.password) {
        setPasswordModal({
          email: row.email,
          password: resp.password,
          title: "New mailbox password (rotation)",
        });
      } else {
        message.success("Password rotated");
      }
    } catch (err) {
      const msg =
        (err as { response?: { data?: { error?: string } } })?.response?.data?.error ??
        "Failed to rotate password";
      message.error(msg);
    } finally {
      setRotatingId(null);
    }
  };

  const openWebmail = (row: MailboxRow) => {
    const popup = window.open("about:blank", "_blank");
    ssoMutation.mutate(
      { id: row.id },
      {
        onSuccess: (data) => {
          if (popup && data?.url) popup.location.href = data.url;
        },
        onError: (err) => {
          const code = (err as { response?: { data?: { error?: string } } })?.response
            ?.data?.error;
          popup?.close();
          if (code === "sso_unavailable_rotate_password") {
            message.error(
              "Rotate the mailbox password first — SSO material is populated on rotation.",
            );
          } else {
            message.error("Failed to open webmail");
          }
        },
      },
    );
  };

  const loading = loadingDomains || mailboxResults.some((r) => r.isLoading);

  if (loading && rows.length === 0) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }

  if (rows.length === 0) {
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No mailboxes yet" />;
  }

  return (
    <>
      <Table<MailboxRow>
        rowKey="id"
        loading={loading && rows.length === 0}
        dataSource={rows}
        pagination={{ pageSize: 20, showSizeChanger: true }}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No mailboxes" /> }}
        columns={[
          {
            title: "Email",
            dataIndex: "email",
            sorter: (a, b) => a.email.localeCompare(b.email),
            render: (v: string) => (
              <Typography.Text style={{ fontFamily: "monospace" }}>
                {v}
              </Typography.Text>
            ),
          },
          {
            title: "Domain",
            dataIndex: "domain_name",
            sorter: (a, b) => a.domain_name.localeCompare(b.domain_name),
            width: 220,
          },
          {
            title: "Quota",
            dataIndex: "quota_bytes",
            width: 220,
            render: (quota: number, row) => {
              const used = row.last_usage_bytes ?? 0;
              const pct =
                quota > 0 ? Math.min(100, Math.round((used / quota) * 100)) : 0;
              return (
                <Tooltip title={`${formatBytes(used)} of ${formatBytes(quota)}`}>
                  <Progress
                    percent={pct}
                    size="small"
                    status={pct >= 90 ? "exception" : "normal"}
                    format={() => `${formatBytes(used)} / ${formatBytes(quota)}`}
                  />
                </Tooltip>
              );
            },
          },
          {
            title: "Last usage",
            dataIndex: "last_usage_at",
            width: 160,
            render: (v: string | null | undefined) =>
              v ? (
                new Date(v).toLocaleString()
              ) : (
                <Typography.Text type="secondary">never</Typography.Text>
              ),
          },
          {
            title: "Status",
            dataIndex: "is_disabled",
            width: 100,
            render: (disabled: boolean) =>
              disabled ? (
                <Tag color="red">disabled</Tag>
              ) : (
                <Tag color="green">active</Tag>
              ),
          },
          {
            title: "Actions",
            width: 180,
            render: (_, row) => (
              <Space>
                <Tooltip title="Open webmail for this mailbox">
                  <Button
                    type="text"
                    icon={<MailOutlined />}
                    loading={
                      ssoMutation.isPending && ssoMutation.variables?.id === row.id
                    }
                    onClick={() => openWebmail(row)}
                  />
                </Tooltip>
                <Tooltip title="Rotate password">
                  <Button
                    type="text"
                    icon={<KeyOutlined />}
                    loading={rotatingId === row.id}
                    onClick={() => rotate(row)}
                  />
                </Tooltip>
                <Popconfirm
                  title={`Delete ${row.email}?`}
                  description="All mail in this mailbox will be removed. This cannot be undone."
                  onConfirm={async () => {
                    try {
                      await deleteMutation.mutateAsync({
                        id: row.id,
                        domainId: row.domain_id,
                      });
                      message.success("Mailbox deleted");
                    } catch (err) {
                      const msg =
                        (err as { response?: { data?: { detail?: string } } })
                          ?.response?.data?.detail ?? "Failed to delete";
                      message.error(msg);
                    }
                  }}
                  okText="Delete"
                  okType="danger"
                >
                  <RowActionButton danger icon={<DeleteOutlined />}>Remove</RowActionButton>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      {passwordModal && (
        <DatabaseUserPasswordModal
          open={true}
          username={passwordModal.email}
          password={passwordModal.password}
          title={passwordModal.title}
          onClose={() => setPasswordModal(null)}
        />
      )}
    </>
  );
};
