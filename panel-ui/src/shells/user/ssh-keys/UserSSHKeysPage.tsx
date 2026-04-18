import { useState } from "react";
import {
  Button,
  Space,
  Table,
  Typography,
  Modal,
  Form,
  Input,
  message,
  Popconfirm,
  Tooltip,
} from "antd";
import {
  PlusSquareOutlined,
  DeleteOutlined,
} from "@ant-design/icons";
import { listSSHKeys, createSSHKey, deleteSSHKey, type SSHKey } from "../../../apiClient";
import { useQuery } from "@tanstack/react-query";

const SSH_KEY_PREFIXES = ["ssh-rsa ", "ssh-ed25519 ", "ecdsa-sha2-"];

export const UserSSHKeysPage = () => {
  const [form] = Form.useForm();
  const [modalOpen, setModalOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  // Fetch SSH keys using react-query
  const { data: listResponse = { items: [] }, isLoading, refetch } = useQuery({
    queryKey: ["ssh-keys"],
    queryFn: async () => listSSHKeys(),
  });

  const keys = listResponse.items || [];

  // Validate that public key starts with a known prefix
  const validatePublicKey = (value: string): string | undefined => {
    if (!value) return undefined;
    const hasValidPrefix = SSH_KEY_PREFIXES.some((prefix) => value.trim().startsWith(prefix));
    if (!hasValidPrefix) {
      return "Public key must start with ssh-rsa, ssh-ed25519, or ecdsa-sha2-";
    }
    return undefined;
  };

  const handleAddKey = async (values: { name: string; public_key: string }) => {
    setLoading(true);
    try {
      // Client-side validation
      const keyError = validatePublicKey(values.public_key);
      if (keyError) {
        message.error(keyError);
        return;
      }

      await createSSHKey({
        name: values.name,
        public_key: values.public_key,
      });

      message.success("SSH key added successfully");
      form.resetFields();
      setModalOpen(false);
      refetch();
    } catch (error: unknown) {
      const err = error as any;
      if (err?.response?.data?.error === "invalid_key") {
        message.error(
          "The public key could not be parsed. Make sure you paste the line from ~/.ssh/id_ed25519.pub (or similar), not a private key.",
        );
      } else if (err?.response?.data?.error === "duplicate_key") {
        message.error("This key is already registered.");
      } else {
        const msg = err?.message ?? "Failed to add SSH key";
        message.error(msg);
      }
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (key: SSHKey) => {
    setDeletingId(key.id);
    try {
      await deleteSSHKey(key.id);
      message.success("SSH key deleted successfully");
      refetch();
    } catch (error) {
      const msg = (error as any)?.message ?? "Failed to delete SSH key";
      message.error(msg);
    } finally {
      setDeletingId(null);
    }
  };

  const truncateFingerprint = (fp: string): string => {
    if (fp.length <= 16) return fp;
    return fp.substring(0, 16) + "…";
  };

  return (
    <div style={{ padding: 24 }}>
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          SSH Keys
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => setModalOpen(true)}
        >
          Add Key
        </Button>
      </Space>

      <Table<SSHKey>
        dataSource={keys}
        loading={isLoading || deletingId !== null}
        rowKey="id"
        bordered
        pagination={false}
        columns={[
          {
            title: "Name",
            dataIndex: "name",
            sorter: (a, b) => a.name.localeCompare(b.name),
            defaultSortOrder: "ascend",
          },
          {
            title: "Fingerprint",
            dataIndex: "fingerprint",
            render: (fingerprint: string) => (
              <Tooltip title={fingerprint}>
                <span style={{ fontFamily: "monospace", fontSize: "12px" }}>
                  {truncateFingerprint(fingerprint)}
                </span>
              </Tooltip>
            ),
          },
          {
            title: "Created",
            dataIndex: "created_at",
            sorter: (a, b) =>
              new Date(a.created_at).getTime() -
              new Date(b.created_at).getTime(),
            render: (date: string) => new Date(date).toLocaleDateString(),
          },
          {
            title: "Actions",
            dataIndex: "actions",
            render: (_, record) => (
              <Popconfirm
                title="Delete SSH Key"
                description="Are you sure? This revokes SFTP access."
                onConfirm={() => handleDelete(record)}
                okText="Yes"
                cancelText="No"
              >
                <Button
                  type="text"
                  danger
                  size="small"
                  icon={<DeleteOutlined />}
                  loading={deletingId === record.id}
                  disabled={deletingId !== null && deletingId !== record.id}
                >
                  Delete
                </Button>
              </Popconfirm>
            ),
          },
        ]}
      />

      <Modal
        title="Add SSH Key"
        open={modalOpen}
        onCancel={() => {
          setModalOpen(false);
          form.resetFields();
        }}
        footer={null}
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={handleAddKey}
        >
          <Form.Item
            label="Name"
            name="name"
            rules={[
              { required: true, message: "Please enter a name" },
              { max: 128, message: "Name must be 128 characters or less" },
            ]}
          >
            <Input placeholder="e.g., My Laptop" />
          </Form.Item>

          <Form.Item
            label="Public Key"
            name="public_key"
            rules={[
              { required: true, message: "Please paste your public key" },
              {
                validator: (_, value) => {
                  const error = validatePublicKey(value);
                  return error ? Promise.reject(error) : Promise.resolve();
                },
              },
            ]}
          >
            <Input.TextArea
              placeholder="ssh-ed25519 AAAA... user@host"
              rows={4}
            />
          </Form.Item>

          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading} block>
              Add Key
            </Button>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};
