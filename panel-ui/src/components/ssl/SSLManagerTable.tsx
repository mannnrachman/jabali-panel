import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Table,
  Tag,
  Button,
  Popconfirm,
  message,
  Empty,
  Space,
  Tooltip,
} from "antd";
import { ReloadOutlined, DeleteOutlined, SyncOutlined, WarningOutlined, RedoOutlined } from "@ant-design/icons";
import { apiClient } from "../../apiClient";

interface SSLCertificate {
  id: string;
  domain_id: string;
  domain_name: string;
  user_id: string;
  user_username: string;
  status: "pending" | "issuing" | "issued" | "renewing" | "revoked" | "failed" | "self_signed" | "pending_acme_retry";
  issued_at: string | null;
  expires_at: string | null;
  renewal_count: number;
  last_renewed_at: string | null;
  last_error: string | null;
  staging: boolean;
  next_retry_at: string | null;
  retry_count: number;
}

interface SSLManagerTableProps {
  endpoint: string;
  showOwner: boolean;
}

const STATUS_COLORS: Record<string, string> = {
  issued: "green",
  issuing: "processing",
  renewing: "processing",
  pending: "default",
  revoked: "default",
  failed: "red",
  self_signed: "orange",
  pending_acme_retry: "gold",
};

const STATUS_ICONS: Record<string, JSX.Element | null> = {
  issuing: <SyncOutlined spin />,
  renewing: <SyncOutlined spin />,
  pending: null,
  issued: null,
  revoked: null,
  failed: null,
  self_signed: <WarningOutlined />,
  pending_acme_retry: <SyncOutlined spin />,
};

const formatDate = (dateStr: string | null): string => {
  if (!dateStr) return "—";
  try {
    const date = new Date(dateStr);
    return date.toLocaleDateString("en-US", {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return "—";
  }
};

const daysUntilExpiry = (expiresAt: string | null): number | null => {
  if (!expiresAt) return null;
  try {
    const expiryDate = new Date(expiresAt);
    const now = new Date();
    const diffMs = expiryDate.getTime() - now.getTime();
    return Math.ceil(diffMs / (1000 * 60 * 60 * 24));
  } catch {
    return null;
  }
};

const formatExpiry = (expiresAt: string | null): JSX.Element => {
  const dateStr = formatDate(expiresAt);
  if (dateStr === "—") return <span>{dateStr}</span>;

  const days = daysUntilExpiry(expiresAt);
  if (days === null) return <span>{dateStr}</span>;

  const isExpiringSoon = days < 14;
  const label =
    days < 0
      ? "expired"
      : days === 0
        ? "today"
        : days === 1
          ? "tomorrow"
          : `in ${days} days`;

  return (
    <span style={{ color: isExpiringSoon ? "#ff4d4f" : "inherit" }}>
      {dateStr} ({label})
    </span>
  );
};

export const SSLManagerTable = ({
  endpoint,
  showOwner,
}: SSLManagerTableProps) => {
  const queryClient = useQueryClient();

  // Fetch SSL certificates
  const { data, isLoading, error } = useQuery({
    queryKey: ["ssl-manager", endpoint],
    queryFn: async () => {
      const response = await apiClient.get(endpoint);
      return response.data.items as SSLCertificate[];
    },
  });

  // Renew certificate mutation
  const renewMutation = useMutation({
    mutationFn: async (domainId: string) => {
      await apiClient.post(`/domains/${domainId}/ssl/renew`);
    },
    onSuccess: () => {
      message.success("Certificate renewal initiated");
      queryClient.invalidateQueries({ queryKey: ["ssl-manager", endpoint] });
    },
    onError: () => {
      message.error("Failed to renew certificate");
    },
  });

  // Revoke certificate mutation
  const revokeMutation = useMutation({
    mutationFn: async (domainId: string) => {
      await apiClient.delete(`/domains/${domainId}/ssl`);
    },
    onSuccess: () => {
      message.success("Certificate revoked");
      queryClient.invalidateQueries({ queryKey: ["ssl-manager", endpoint] });
    },
    onError: () => {
      message.error("Failed to revoke certificate");
    },
  });

  if (error) {
    return (
      <Empty
        description="Failed to load SSL certificates"
        style={{ marginTop: 48 }}
      />
    );
  }

  const columns = [
    {
      title: "Domain",
      dataIndex: "domain_name",
      key: "domain_name",
      render: (text: string) => (
        <span style={{ fontFamily: "monospace", fontSize: "12px" }}>
          {text}
        </span>
      ),
    },
    ...(showOwner
      ? [
          {
            title: "Owner",
            dataIndex: "user_username",
            key: "user_username",
          },
        ]
      : []),
    {
      title: "Status",
      dataIndex: "status",
      key: "status",
      render: (status: string) => (
        <Tag
          color={STATUS_COLORS[status] || "default"}
          icon={STATUS_ICONS[status]}
        >
          {status.charAt(0).toUpperCase() + status.slice(1)}
        </Tag>
      ),
    },
    {
      title: "Issued",
      dataIndex: "issued_at",
      key: "issued_at",
      render: (dateStr: string | null) => formatDate(dateStr),
    },
    {
      title: "Expires",
      dataIndex: "expires_at",
      key: "expires_at",
      render: (dateStr: string | null) => formatExpiry(dateStr),
    },
    {
      title: "Staging",
      dataIndex: "staging",
      key: "staging",
      render: (isStaging: boolean) =>
        isStaging ? <Tag color="blue">staging</Tag> : null,
    },
    {
      title: "Actions",
      key: "actions",
      render: (_: unknown, record: SSLCertificate) => (
        <Space>
          <Tooltip title="Renew certificate">
            <Button
              type="primary"
              size="small"
              icon={<ReloadOutlined />}
              loading={renewMutation.isPending}
              onClick={() => renewMutation.mutate(record.domain_id)}
            />
          </Tooltip>
          <Popconfirm
            title="Revoke Certificate"
            description="Are you sure you want to revoke this certificate?"
            onConfirm={() => revokeMutation.mutate(record.domain_id)}
            okText="Yes"
            cancelText="No"
          >
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              loading={revokeMutation.isPending}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      {!data || data.length === 0 ? (
        <Empty
          description="No SSL certificates yet"
          style={{ marginTop: 48 }}
        />
      ) : (
        <Table
          dataSource={data}
          columns={columns}
          rowKey="id"
          loading={isLoading}
          bordered
          pagination={{ pageSize: 25, showSizeChanger: true }}
        />
      )}
    </>
  );
};
