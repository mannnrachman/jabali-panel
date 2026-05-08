// AdminAutomationTokensPage — admin Automation API token list +
// mint Drawer + one-time-secret reveal Modal (M44).
//
// Tokens are HMAC bearers external automations use to hit the
// /api/v1/automation/* read-only surface. Plaintext secret is
// exposed exactly once — at mint time — via a Modal with a
// copy-to-clipboard button and a "save it now" warning.
//
// Revocation is soft (sets revoked_at). Operators can audit which
// admin minted what + when it was last used.
import { useState } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Drawer,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { KeyOutlined, PlusOutlined, DeleteOutlined } from "@icons";

import { apiClient } from "../../../apiClient";

type Token = {
  id: string;
  name: string;
  scopes: string[];
  created_at: string;
  last_used_at?: string | null;
  last_used_ip?: string | null;
  revoked_at?: string | null;
};

type ListResp = { data: Token[]; total: number };
type MintResp = Token & { secret: string };

const SCOPE_OPTIONS = [
  { value: "read:*", label: "read:* (everything below)" },
  { value: "read:domains", label: "read:domains" },
  { value: "read:users", label: "read:users" },
  { value: "read:applications", label: "read:applications" },
  { value: "read:status", label: "read:status" },
];

function fmt(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export const AdminAutomationTokensPage = () => {
  const qc = useQueryClient();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [revealSecret, setRevealSecret] = useState<string | null>(null);
  const [revealName, setRevealName] = useState<string | null>(null);
  const [form] = Form.useForm<{ name: string; scopes: string[] }>();

  const list = useQuery<ListResp>({
    queryKey: ["list", "admin/automation/tokens"],
    queryFn: async () => {
      const { data } = await apiClient.get<ListResp>("/admin/automation/tokens");
      return data;
    },
  });

  const mint = useMutation<MintResp, unknown, { name: string; scopes: string[] }>({
    mutationFn: async (input) => {
      const { data } = await apiClient.post<MintResp>("/admin/automation/tokens", input);
      return data;
    },
    onSuccess: async (resp) => {
      await qc.invalidateQueries({ queryKey: ["list", "admin/automation/tokens"] });
      setDrawerOpen(false);
      form.resetFields();
      setRevealName(resp.name);
      setRevealSecret(resp.secret);
    },
  });

  const revoke = useMutation<unknown, unknown, { id: string }>({
    mutationFn: async ({ id }) => {
      await apiClient.delete(`/admin/automation/tokens/${id}`);
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["list", "admin/automation/tokens"] });
    },
  });

  const handleMint = async (values: { name: string; scopes: string[] }) => {
    try {
      await mint.mutateAsync(values);
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Mint failed");
    }
  };

  const copySecret = async () => {
    if (!revealSecret) return;
    try {
      await navigator.clipboard.writeText(revealSecret);
      message.success("Secret copied to clipboard");
    } catch {
      message.error("Clipboard access blocked — copy manually");
    }
  };

  return (
    <div>
      <Typography.Title level={2}>
        <Space>
          <KeyOutlined /> Automation API Tokens
        </Space>
      </Typography.Title>
      <Typography.Paragraph type="secondary">
        HMAC-signed bearer tokens for external automations (CI scripts, monitoring,
        partner integrations). Every request signs <code>METHOD || PATH || TS || SHA256(BODY)</code>{" "}
        with the per-token secret. Tokens are scoped — issue the narrowest scope set the caller
        needs.
      </Typography.Paragraph>

      <Card
        extra={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setDrawerOpen(true)}
          >
            Mint Token
          </Button>
        }
      >
        <Table<Token>
          rowKey="id"
          loading={list.isLoading}
          dataSource={list.data?.data ?? []}
          pagination={false}
          scroll={{ x: "max-content" }}
        >
          <Table.Column<Token> title="Name" dataIndex="name" />
          <Table.Column<Token>
            title="Scopes"
            render={(_, r) => (
              <Space wrap size={4}>
                {r.scopes.map((s) => (
                  <Tag key={s} color={s === "read:*" ? "purple" : "blue"}>
                    {s}
                  </Tag>
                ))}
              </Space>
            )}
          />
          <Table.Column<Token>
            title="Created"
            dataIndex="created_at"
            render={(v: string) => fmt(v)}
          />
          <Table.Column<Token>
            title="Last Used"
            render={(_, r) => (
              <Space direction="vertical" size={0}>
                <span>{fmt(r.last_used_at)}</span>
                {r.last_used_ip && (
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {r.last_used_ip}
                  </Typography.Text>
                )}
              </Space>
            )}
          />
          <Table.Column<Token>
            title="Status"
            render={(_, r) =>
              r.revoked_at ? (
                <Tag color="red">revoked {fmt(r.revoked_at)}</Tag>
              ) : (
                <Tag color="green">active</Tag>
              )
            }
          />
          <Table.Column<Token>
            title="Actions"
            render={(_, r) =>
              r.revoked_at ? (
                <Typography.Text type="secondary">—</Typography.Text>
              ) : (
                <Popconfirm
                  title={`Revoke token "${r.name}"?`}
                  description="External callers using this token will start failing immediately. Revoke is soft (audit-only); the row stays in this list with a 'revoked' tag."
                  okText="Revoke"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => revoke.mutateAsync({ id: r.id })}
                >
                  <Button danger icon={<DeleteOutlined />} variant="filled" color="danger">
                    Revoke
                  </Button>
                </Popconfirm>
              )
            }
          />
        </Table>
      </Card>

      <Drawer
        title="Mint Automation API Token"
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        width={500}
        destroyOnClose
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={handleMint}
          initialValues={{ scopes: ["read:status"] }}
        >
          <Form.Item
            name="name"
            label="Name"
            rules={[
              { required: true, message: "Required" },
              { max: 100 },
            ]}
            extra="Human-readable label. Tokens are unique by name."
          >
            <Input placeholder="e.g. monitoring-bot, ci-deploy" />
          </Form.Item>

          <Form.Item
            name="scopes"
            label="Scopes"
            rules={[{ required: true, message: "At least one scope" }]}
            extra="Wildcard 'read:*' grants every read; otherwise tick only the resources the automation needs."
          >
            <Checkbox.Group options={SCOPE_OPTIONS} style={{ display: "flex", flexDirection: "column", gap: 8 }} />
          </Form.Item>

          <Space>
            <Button type="primary" htmlType="submit" loading={mint.isPending}>
              Mint
            </Button>
            <Button onClick={() => setDrawerOpen(false)}>Cancel</Button>
          </Space>
        </Form>
      </Drawer>

      <Modal
        open={revealSecret !== null}
        title={`Token "${revealName}" minted`}
        closable={false}
        footer={[
          <Button key="copy" type="primary" onClick={copySecret}>
            Copy to clipboard
          </Button>,
          <Button
            key="done"
            onClick={() => {
              setRevealSecret(null);
              setRevealName(null);
            }}
          >
            I've saved it
          </Button>,
        ]}
        width={600}
      >
        <Alert
          type="warning"
          showIcon
          message="This is the only time the secret will be shown."
          description="Copy it now and store it in your automation's secret manager. The server only keeps an encrypted copy. If you lose it, revoke this token and mint a new one."
          style={{ marginBottom: 16 }}
        />
        <Typography.Paragraph copyable={{ tooltips: ["Copy", "Copied"] }}>
          <code style={{ wordBreak: "break-all", display: "block", padding: 12, background: "#fafafa", border: "1px solid #d9d9d9", borderRadius: 4 }}>
            {revealSecret}
          </code>
        </Typography.Paragraph>
      </Modal>

    </div>
  );
};
