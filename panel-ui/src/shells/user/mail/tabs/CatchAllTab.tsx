// CatchAllTab — M6.5 Step 2. Cross-domain catch-all overview.
//
// Lists all email-enabled domains + their catch-all target (if any).
// Create/edit dialog: pick domain, enter target mailbox email.

import { useMemo, useState } from "react";
import {
  Button,
  
  Empty,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Select,
  Skeleton,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { DeleteOutlined, EditOutlined, PlusOutlined } from "@icons";
import { useQueries } from "@tanstack/react-query";

import { apiClient } from "../../../../apiClient";
import { useListQuery } from "../../../../hooks/useQueries";
import {
  useUpdateDomainCatchAll,
  useDeleteDomainCatchAll,
  type DomainCatchAll,
} from "../../../../hooks/useCatchAll";
import type { Domain } from "../../domains/UserDomainList";

interface CatchAllRow {
  domain_id: string;
  domain_name: string;
  target: string | null;
  updated_at: string;
}

export const CatchAllTab = () => {
  const { items: domains, isLoading: loadingDomains } = useListQuery<Domain>({
    resource: "domains",
    params: { page: 1, pageSize: 200, sort: "name", order: "asc" },
  });

  const emailEnabledDomains = useMemo(
    () => domains.filter((d) => d.email_enabled),
    [domains],
  );

  const results = useQueries({
    queries: emailEnabledDomains.map((d) => ({
      queryKey: ["catchall", d.id],
      queryFn: async () => {
        const { data } = await apiClient.get<DomainCatchAll>(
          `/domains/${d.id}/catchall`,
        );
        return data;
      },
    })),
  });

  const anyLoading = results.some((r) => r.isLoading);
  const rows: CatchAllRow[] = useMemo(() => {
    const out: CatchAllRow[] = [];
    for (const r of results) {
      if (r.data) out.push(r.data);
    }
    return out;
  }, [results]);

  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState<CatchAllRow | null>(null);
  const [form] = Form.useForm<{ domain_id: string; target: string }>();

  const updateMut = useUpdateDomainCatchAll();
  const deleteMut = useDeleteDomainCatchAll();

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    setEditOpen(true);
  };
  const openEdit = (row: CatchAllRow) => {
    setEditing(row);
    form.setFieldsValue({ domain_id: row.domain_id, target: row.target ?? "" });
    setEditOpen(true);
  };

  const submit = async () => {
    const vals = await form.validateFields();
    try {
      await updateMut.mutateAsync({ domainID: vals.domain_id, target: vals.target });
      message.success("Catch-all updated");
      setEditOpen(false);
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error
        ?? "Failed to update catch-all";
      message.error(msg);
    }
  };

  if (loadingDomains && domains.length === 0) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }

  if (emailEnabledDomains.length === 0) {
    return <Empty description="No email-enabled domains yet" />;
  }

  return (
    <>
      <div>
        <Space
          style={{
            width: "100%",
            justifyContent: "space-between",
            marginBottom: 12,
            flexWrap: "wrap",
            rowGap: 8,
          }}
        >
          <Typography.Title level={3} style={{ margin: 0 }}>
            Catch-All
          </Typography.Title>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            Set catch-all
          </Button>
        </Space>

        <Table<CatchAllRow>
          rowKey="domain_id"
          loading={anyLoading && rows.length === 0}
          dataSource={rows}
          pagination={false}
          scroll={{ x: "max-content" }}
          columns={[
            {
              title: "Domain",
              dataIndex: "domain_name",
              sorter: (a, b) => a.domain_name.localeCompare(b.domain_name),
            },
            {
              title: "Target",
              dataIndex: "target",
              render: (v: string | null) =>
                v ? (
                  <Typography.Text style={{ fontFamily: "monospace" }}>{v}</Typography.Text>
                ) : (
                  <Typography.Text type="secondary">— (not set)</Typography.Text>
                ),
            },
            {
              title: "Status",
              width: 120,
              render: (_, row) =>
                row.target ? (
                  <Tag color="green">active</Tag>
                ) : (
                  <Tag>inactive</Tag>
                ),
            },
            {
              title: "Actions",
              width: 140,
              render: (_, row) => (
                <Space>
                  <Tooltip title="Edit target">
                    <Button type="text" icon={<EditOutlined />} onClick={() => openEdit(row)} />
                  </Tooltip>
                  {row.target && (
                    <Popconfirm
                      title={`Clear catch-all for ${row.domain_name}?`}
                      onConfirm={async () => {
                        try {
                          await deleteMut.mutateAsync(row.domain_id);
                          message.success("Catch-all cleared");
                        } catch (err) {
                          const msg = (err as { response?: { data?: { error?: string } } })?.response
                            ?.data?.error ?? "Failed to clear";
                          message.error(msg);
                        }
                      }}
                      okText="Clear"
                      okButtonProps={{ danger: true }}
                    >
                      <Button type="text" danger icon={<DeleteOutlined />} />
                    </Popconfirm>
                  )}
                </Space>
              ),
            },
          ]}
        />
      </div>

      <Modal
        open={editOpen}
        title={editing ? `Catch-all: ${editing.domain_name}` : "Set catch-all"}
        onCancel={() => setEditOpen(false)}
        onOk={submit}
        okText="Save"
        confirmLoading={updateMut.isPending}
        destroyOnClose
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="domain_id"
            label="Domain"
            rules={[{ required: true, message: "Select a domain" }]}
          >
            <Select
              placeholder="Select email-enabled domain"
              disabled={!!editing}
              options={emailEnabledDomains.map((d) => ({ label: d.name, value: d.id }))}
            />
          </Form.Item>
          <Form.Item
            name="target"
            label="Target mailbox"
            rules={[
              { required: true, message: "Enter an email address" },
              { type: "email", message: "Invalid email" },
            ]}
            extra="Mail sent to unknown addresses at this domain is delivered to this mailbox."
          >
            <Input placeholder="admin@example.com" autoFocus />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};
