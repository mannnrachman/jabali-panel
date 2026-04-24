// AutorespondersTab — M6.5 Step 3. Cross-domain vacation responses.
//
// Lists mailboxes + their autoresponder status. Set dialog configures
// dates, subject, and plain/HTML body for a vacation response.

import { useMemo, useState } from "react";
import {
  Button,
  Card,
  DatePicker,
  Empty,
  Form,
  Input,
  message,
  Modal,
  Popconfirm,
  Select,
  Skeleton,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import { DeleteOutlined, EditOutlined, PlusOutlined } from "@icons";
import { useQueries } from "@tanstack/react-query";
import dayjs, { type Dayjs } from "dayjs";

import { apiClient } from "../../../../apiClient";
import { useListQuery } from "../../../../hooks/useQueries";
import {
  useUpdateAutoresponder,
  useDeleteAutoresponder,
  type Autoresponder,
} from "../../../../hooks/useAutoresponders";
import type { Domain } from "../../domains/UserDomainList";

interface Mailbox {
  id: string;
  email: string;
  domain_id: string;
  local_part: string;
}

interface ARRow extends Autoresponder {
  mailbox_email: string;
  domain_name: string;
}

interface FormValues {
  mailbox_id: string;
  enabled: boolean;
  date_range?: [Dayjs | null, Dayjs | null];
  subject?: string;
  text_body?: string;
  html_body?: string;
}

export const AutorespondersTab = () => {
  const { items: domains, isLoading: loadingDomains } = useListQuery<Domain>({
    resource: "domains",
    params: { page: 1, pageSize: 200, sort: "name", order: "asc" },
  });

  const emailEnabledDomains = useMemo(
    () => domains.filter((d) => d.email_enabled),
    [domains],
  );

  // Fan out: mailbox list per email-enabled domain.
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

  const mailboxes = useMemo(() => {
    const out: { mb: Mailbox; dom: Domain }[] = [];
    for (const r of mailboxResults) {
      if (!r.data) continue;
      for (const mb of r.data.items) {
        out.push({ mb, dom: r.data.domain });
      }
    }
    return out;
  }, [mailboxResults]);

  // One autoresponder fetch per mailbox.
  const arResults = useQueries({
    queries: mailboxes.map(({ mb, dom }) => ({
      queryKey: ["autoresponder", mb.id],
      queryFn: async () => {
        const { data } = await apiClient.get<Autoresponder>(
          `/mailboxes/${mb.id}/autoresponder`,
        );
        return {
          ...data,
          mailbox_email: mb.email,
          domain_name: dom.name,
        } as ARRow;
      },
    })),
  });

  const anyLoading = mailboxResults.some((r) => r.isLoading) || arResults.some((r) => r.isLoading);
  // Autoresponder GET returns a default {enabled:false, updated_at:0001-01-01}
  // shape when nothing has ever been saved for a mailbox. Filter those out so
  // each mailbox only surfaces when an autoresponder actually exists.
  const rows = useMemo(
    () =>
      arResults
        .filter((r) => r.data)
        .map((r) => r.data as ARRow)
        .filter((row) => row.enabled || (row.updated_at && !row.updated_at.startsWith("0001-01-01"))),
    [arResults],
  );

  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState<ARRow | null>(null);
  const [form] = Form.useForm<FormValues>();

  const updateMut = useUpdateAutoresponder();
  const deleteMut = useDeleteAutoresponder();

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ enabled: true });
    setEditOpen(true);
  };

  const openEdit = (row: ARRow) => {
    setEditing(row);
    form.setFieldsValue({
      mailbox_id: row.mailbox_id,
      enabled: row.enabled,
      date_range: [
        row.from_date ? dayjs(row.from_date) : null,
        row.to_date ? dayjs(row.to_date) : null,
      ],
      subject: row.subject ?? "",
      text_body: row.text_body ?? "",
      html_body: row.html_body ?? "",
    });
    setEditOpen(true);
  };

  const submit = async () => {
    const vals = await form.validateFields();
    const [from, to] = vals.date_range ?? [null, null];
    try {
      await updateMut.mutateAsync({
        mailboxID: vals.mailbox_id,
        input: {
          enabled: vals.enabled,
          from_date: from ? from.toISOString() : null,
          to_date: to ? to.toISOString() : null,
          subject: vals.subject || null,
          text_body: vals.text_body || null,
          html_body: vals.html_body || null,
        },
      });
      message.success("Autoresponder saved");
      setEditOpen(false);
    } catch (err) {
      const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error
        ?? "Failed to save autoresponder";
      message.error(msg);
    }
  };

  if (loadingDomains && domains.length === 0) {
    return <Skeleton active paragraph={{ rows: 4 }} />;
  }

  if (emailEnabledDomains.length === 0 || mailboxes.length === 0) {
    return (
      <Card>
        <Empty description="Create a mailbox first to set an autoresponder" />
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
            Autoresponders
          </Typography.Title>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            Set autoresponder
          </Button>
        </Space>

        <Table<ARRow>
          rowKey="mailbox_id"
          loading={anyLoading && rows.length === 0}
          dataSource={rows}
          pagination={{ pageSize: 20 }}
          scroll={{ x: "max-content" }}
          columns={[
            {
              title: "Mailbox",
              dataIndex: "mailbox_email",
              sorter: (a, b) => a.mailbox_email.localeCompare(b.mailbox_email),
              render: (v: string) => (
                <Typography.Text style={{ fontFamily: "monospace" }}>{v}</Typography.Text>
              ),
            },
            {
              title: "Status",
              width: 110,
              render: (_, row) =>
                row.enabled ? (
                  <Tag color="green">active</Tag>
                ) : (
                  <Tag>inactive</Tag>
                ),
            },
            {
              title: "Date range",
              width: 260,
              render: (_, row) => {
                if (!row.from_date && !row.to_date) return <Typography.Text type="secondary">always</Typography.Text>;
                const f = row.from_date ? new Date(row.from_date).toLocaleDateString() : "—";
                const t = row.to_date ? new Date(row.to_date).toLocaleDateString() : "—";
                return `${f} → ${t}`;
              },
            },
            {
              title: "Subject",
              dataIndex: "subject",
              render: (v: string | null) => v ?? <Typography.Text type="secondary">(default)</Typography.Text>,
            },
            {
              title: "Actions",
              width: 140,
              render: (_, row) => (
                <Space>
                  <Tooltip title="Edit">
                    <Button type="text" icon={<EditOutlined />} onClick={() => openEdit(row)} />
                  </Tooltip>
                  <Popconfirm
                    title={`Disable autoresponder for ${row.mailbox_email}?`}
                    onConfirm={async () => {
                      try {
                        await deleteMut.mutateAsync(row.mailbox_id);
                        message.success("Autoresponder removed");
                      } catch (err) {
                        const msg = (err as { response?: { data?: { error?: string } } })?.response?.data?.error
                          ?? "Failed to remove";
                        message.error(msg);
                      }
                    }}
                    okText="Remove"
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

      <Modal
        open={editOpen}
        title={editing ? `Autoresponder: ${editing.mailbox_email}` : "Set autoresponder"}
        onCancel={() => setEditOpen(false)}
        onOk={submit}
        okText="Save"
        confirmLoading={updateMut.isPending}
        destroyOnClose
        width={640}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="mailbox_id"
            label="Mailbox"
            rules={[{ required: true, message: "Select a mailbox" }]}
          >
            <Select
              placeholder="Select a mailbox"
              disabled={!!editing}
              showSearch
              optionFilterProp="label"
              options={mailboxes.map(({ mb }) => ({ label: mb.email, value: mb.id }))}
            />
          </Form.Item>
          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="date_range" label="Active dates (optional)">
            <DatePicker.RangePicker showTime style={{ width: "100%" }} />
          </Form.Item>
          <Form.Item name="subject" label="Subject">
            <Input placeholder="Out of office" maxLength={200} />
          </Form.Item>
          <Form.Item name="text_body" label="Plain text body">
            <Input.TextArea rows={4} placeholder="I'm away until..." />
          </Form.Item>
          <Form.Item name="html_body" label="HTML body (optional)">
            <Input.TextArea rows={4} placeholder="<p>I'm away until...</p>" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};
