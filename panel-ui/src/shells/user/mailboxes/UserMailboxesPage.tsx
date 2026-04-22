// UserMailboxesPage — tenant-scoped cross-domain mailbox management.
//
// This page is the single place a user manages mail accounts. It lists
// every mailbox across every email-enabled domain the user owns in one
// table, and a single "Create mailbox" button at the top opens a
// wizard-style modal whose first step is picking the target domain.
//
// Architecture note: rather than adding a cross-domain list endpoint
// to panel-api, we fan out GET /domains/:id/mailboxes across the
// user's email-enabled domains via TanStack's useQueries and merge
// client-side. At the scale of a hosted tenant (usually <20 domains,
// each with <50 mailboxes), this is fine and avoids a new backend
// surface. If it becomes a hotspot we can swap in a unified endpoint
// behind the same hook.
import { useMemo, useState } from "react";
import {
  Button,
  Card,
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
  PlusOutlined,
} from "@ant-design/icons";
import { useQueries } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";
import { DatabaseUserPasswordModal } from "../../../components/DatabaseUserPasswordModal";
import {
  useDeleteMailbox,
  useMintMailboxSSO,
  useRotateMailboxPassword,
  type Mailbox,
} from "../../../hooks/useMailboxes";
import { useListQuery } from "../../../hooks/useQueries";
import type { Domain } from "../domains/UserDomainList";
import { CreateMailboxWizardModal } from "./CreateMailboxWizardModal";

// Flat row embeds the owning domain's name/id so the table can render
// and sort by domain without re-deriving from email_cached every time.
type MailboxRow = Mailbox & { domain_name: string };

function formatBytes(n: number): string {
  if (n === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const i = Math.floor(Math.log(n) / Math.log(1024));
  const v = n / Math.pow(1024, i);
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export const UserMailboxesPage = () => {
  const { items: domains, isLoading: loadingDomains } = useListQuery<Domain>({
    resource: "domains",
    params: { page: 1, pageSize: 200, sort: "name", order: "asc" },
  });

  const emailEnabledDomains = useMemo(
    () => domains.filter((d) => d.email_enabled),
    [domains],
  );

  // Fan out one list query per email-enabled domain. TanStack caches
  // each under the same queryKey shape as useMailboxes, so the Create
  // mutation's onSuccess invalidation still flows through here.
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

  const anyMailboxLoading = mailboxResults.some((r) => r.isLoading);

  // Merge mailboxes from every domain into one flat list the table
  // consumes. Each row carries `domain_name` so the Domain column can
  // render without parsing email_cached (we still use the cached email
  // for display + sorting on the Email column).
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

  const [createOpen, setCreateOpen] = useState(false);
  const [passwordModal, setPasswordModal] = useState<{
    email: string;
    password: string;
    title: string;
  } | null>(null);
  const [rotatingId, setRotatingId] = useState<string | null>(null);

  const deleteMutation = useDeleteMailbox();
  const rotateMutation = useRotateMailboxPassword();
  const ssoMutation = useMintMailboxSSO();

  // --- Row actions ---

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

  // Popup-blocker mitigation mirrors DomainMailboxesSection: open a
  // blank tab synchronously from the user click, then navigate it
  // once the mint response arrives.
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

  // --- Empty / loading ---

  if (loadingDomains && domains.length === 0) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }

  if (domains.length === 0) {
    return (
      <Card>
        <Empty
          image={<MailOutlined style={{ fontSize: 48, color: "#bbb" }} />}
          description={
            <>
              <Typography.Title level={5} style={{ marginBottom: 4 }}>
                No domains yet
              </Typography.Title>
              <Typography.Text type="secondary">
                Add a domain before setting up mail.
              </Typography.Text>
            </>
          }
        />
      </Card>
    );
  }

  const disableCreate = emailEnabledDomains.length === 0;

  return (
    <>
      <Card>
        <Space
          style={{ width: "100%", justifyContent: "space-between", marginBottom: 12 }}
        >
          <Typography.Title level={3} style={{ margin: 0 }}>
            <MailOutlined /> Mailboxes
          </Typography.Title>
          <Tooltip
            title={
              disableCreate
                ? "None of your domains have email enabled yet."
                : undefined
            }
          >
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setCreateOpen(true)}
              disabled={disableCreate}
            >
              Create mailbox
            </Button>
          </Tooltip>
        </Space>

        <Table<MailboxRow>
          rowKey="id"
          loading={anyMailboxLoading && rows.length === 0}
          dataSource={rows}
          pagination={{ pageSize: 20, showSizeChanger: true }}
          locale={{ emptyText: <Empty description="No mailboxes yet" /> }}
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
                    okButtonProps={{ danger: true }}
                  >
                    <Button type="text" danger icon={<DeleteOutlined />} />
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <CreateMailboxWizardModal
        open={createOpen}
        domains={emailEnabledDomains.map((d) => ({ id: d.id, name: d.name }))}
        onCancel={() => setCreateOpen(false)}
        onCreated={({ email, password }) => {
          setCreateOpen(false);
          if (password) {
            setPasswordModal({
              email,
              password,
              title: "New mailbox password",
            });
          } else {
            message.success(`Mailbox ${email} created`);
          }
        }}
      />

      {passwordModal && (
        <DatabaseUserPasswordModal
          open
          title={passwordModal.title}
          username={passwordModal.email}
          password={passwordModal.password}
          onClose={() => setPasswordModal(null)}
        />
      )}
    </>
  );
};
