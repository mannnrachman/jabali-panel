// ForwardersTab — M6.5 Step 5. Two-flavor forwarders (alias + external).

import { useMemo, useState } from "react";
import {
  Button,
  Card,
  Empty,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Radio,
  Select,
  Skeleton,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { useQueries } from "@tanstack/react-query";

import { apiClient } from "../../../../apiClient";
import { useListQuery } from "../../../../hooks/useQueries";
import {
  useForwarders,
  useCreateForwarder,
  useDeleteForwarder,
} from "../../../../hooks/useForwarders";
import type { Domain } from "../../domains/UserDomainList";

interface Mailbox {
  id: string;
  email_cached: string;
  domain_id: string;
}

interface FormValues {
  mailbox_id: string;
  type: "alias" | "external";
  local_part?: string;
  target: string;
}

export const ForwardersTab = () => {
  const { items: domains, isLoading: loadingDomains } = useListQuery<Domain>({
    resource: "domains",
    params: { page: 1, pageSize: 200, sort: "name", order: "asc" },
  });
  const emailEnabled = useMemo(() => domains.filter((d) => d.email_enabled), [domains]);

  const mailboxResults = useQueries({
    queries: emailEnabled.map((d) => ({
      queryKey: ["list", "mailboxes", d.id, { page: 1, pageSize: 200 }],
      queryFn: async () => {
        const { data } = await apiClient.get<{ data: Mailbox[]; total: number }>(
          `/domains/${d.id}/mailboxes?page=1&page_size=200&sort=local_part&order=asc`,
        );
        return { items: data.data ?? [], domain: d };
      },
    })),
  });

  const mailboxes = useMemo(() => {
    const out: Mailbox[] = [];
    for (const r of mailboxResults) {
      if (!r.data) continue;
      out.push(...r.data.items);
    }
    return out;
  }, [mailboxResults]);

  const { data: forwarders = [], isLoading } = useForwarders();
  const createMut = useCreateForwarder();
  const deleteMut = useDeleteForwarder();

  const [open, setOpen] = useState(false);
  const [form] = Form.useForm<FormValues>();
  const type = Form.useWatch("type", form);

  const submit = async () => {
    const vals = await form.validateFields();
    try {
      await createMut.mutateAsync({
        mailboxID: vals.mailbox_id,
        type: vals.type,
        localPart: vals.type === "alias" ? vals.local_part : undefined,
        target: vals.target,
      });
      message.success("Forwarder created");
      setOpen(false);
      form.resetFields();
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error
        ?? "Failed to create forwarder";
      message.error(msg);
    }
  };

  if (loadingDomains && domains.length === 0) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }
  if (mailboxes.length === 0) {
    return (
      <Card>
        <Empty description="Create mailboxes first" />
      </Card>
    );
  }

  return (
    <>
      <Card>
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
            Forwarders
          </Typography.Title>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
            Add forwarder
          </Button>
        </Space>

        <Table
          rowKey="id"
          loading={isLoading}
          dataSource={forwarders}
          pagination={{ pageSize: 20 }}
          scroll={{ x: "max-content" }}
          columns={[
            {
              title: "Source",
              render: (_, row) =>
                row.type === "alias"
                  ? `${row.local_part}@${row.domain_name}`
                  : row.mailbox_email,
            },
            {
              title: "Type",
              dataIndex: "type",
              width: 100,
              render: (v: string) => (
                <Tag color={v === "alias" ? "blue" : "purple"}>{v}</Tag>
              ),
            },
            {
              title: "Target",
              render: (_, row) =>
                row.type === "alias" ? row.mailbox_email : row.target,
            },
            {
              title: "Actions",
              width: 80,
              render: (_, row) => (
                <Popconfirm
                  title="Remove forwarder?"
                  onConfirm={async () => {
                    try {
                      await deleteMut.mutateAsync(row.id);
                      message.success("Forwarder removed");
                    } catch (err) {
                      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error
                        ?? "Failed to remove";
                      message.error(msg);
                    }
                  }}
                  okText="Remove"
                  okButtonProps={{ danger: true }}
                >
                  <Tooltip title="Remove">
                    <Button type="text" danger icon={<DeleteOutlined />} />
                  </Tooltip>
                </Popconfirm>
              ),
            },
          ]}
        />
      </Card>

      <Modal
        open={open}
        title="Add forwarder"
        onCancel={() => setOpen(false)}
        onOk={submit}
        okText="Create"
        confirmLoading={createMut.isPending}
        destroyOnClose
        width={560}
      >
        <Form
          form={form}
          layout="vertical"
          preserve={false}
          initialValues={{ type: "alias" }}
        >
          <Form.Item name="type" label="Type" rules={[{ required: true }]}>
            <Radio.Group>
              <Radio value="alias">Alias (local-part → mailbox)</Radio>
              <Radio value="external">External (mailbox → outside email)</Radio>
            </Radio.Group>
          </Form.Item>

          <Form.Item
            name="mailbox_id"
            label={type === "alias" ? "Target mailbox" : "Source mailbox"}
            rules={[{ required: true }]}
          >
            <Select
              placeholder="Select mailbox"
              showSearch
              optionFilterProp="label"
              options={mailboxes.map((m) => ({ label: m.email_cached, value: m.id }))}
            />
          </Form.Item>

          {type === "alias" && (
            <Form.Item
              name="local_part"
              label="Alias local-part"
              rules={[{ required: true, pattern: /^[a-z0-9._-]+$/, message: "a-z 0-9 . _ -" }]}
              extra="Alias @ source-mailbox's domain. Mail to alias@domain delivers to the mailbox."
            >
              <Input placeholder="sales" autoFocus />
            </Form.Item>
          )}

          {type === "external" && (
            <Form.Item
              name="target"
              label="External target"
              rules={[{ required: true, type: "email", message: "Enter a valid email" }]}
              extra="Mail to the source mailbox is forwarded (copy kept) to this address."
            >
              <Input placeholder="forward-to@example.com" autoFocus />
            </Form.Item>
          )}
        </Form>
      </Modal>
    </>
  );
};
