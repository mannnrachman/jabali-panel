import { useMemo, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Input,
  Table,
  Tag,
  Button,
  Popconfirm,
  message,
  Empty,
  Space,
  Tooltip,
  Typography,
} from "antd";
import { ReloadOutlined, DeleteOutlined, SyncOutlined, WarningOutlined, RedoOutlined } from "@icons";
import { apiClient } from "../../apiClient";
import { columnSearchProps } from "../columnSearch";

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
  last_attempt_at: string | null;
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
    <Typography.Text type={isExpiringSoon ? "danger" : undefined}>
      {dateStr} ({label})
    </Typography.Text>
  );
};

export const SSLManagerTable = ({
  endpoint,
  showOwner,
}: SSLManagerTableProps) => {
  const queryClient = useQueryClient();

  // Client-side search over the fetched rows — SSL list is small
  // enough that we don't need server-side ?q filtering.
  const [search, setSearch] = useState("");

  // Fetch SSL certificates
  const { data, isLoading, error } = useQuery({
    queryKey: ["ssl-manager", endpoint],
    queryFn: async () => {
      const response = await apiClient.get(endpoint);
      return response.data.items as SSLCertificate[];
    },
  });

  const filteredData = useMemo(() => {
    if (!data || !search) return data;
    const needle = search.toLowerCase();
    return data.filter(
      (row) =>
        row.domain_name.toLowerCase().includes(needle) ||
        (row.user_username ?? "").toLowerCase().includes(needle),
    );
  }, [data, search]);

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

  // Retry certificate mutation
  const retryMutation = useMutation({
    mutationFn: async (domainId: string) => {
      await apiClient.post(`/domains/${domainId}/ssl/retry`);
    },
    onSuccess: () => {
      message.success("Retry queued");
      queryClient.invalidateQueries({ queryKey: ["ssl-manager", endpoint] });
    },
    onError: (error: unknown) => {
      const status = (error as { response?: { status?: number; data?: { error?: string } } })?.response;
      if (status?.status === 409) {
        message.info("Already retryable — will attempt on next tick");
      } else {
        message.error("Failed to queue retry");
      }
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
      ...columnSearchProps<SSLCertificate>({
        placeholder: "Search by domain or owner",
        currentQ: search,
        onSearch: (v) => setSearch(v),
      }),
      render: (text: string) => (
        <span style={{ fontFamily: "monospace" }}>
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
            ...columnSearchProps<SSLCertificate>({
              placeholder: "Search by domain or owner",
              currentQ: search,
              onSearch: (v) => setSearch(v),
            }),
          },
        ]
      : []),
    {
      title: "Status",
      dataIndex: "status",
      key: "status",
      render: (status: string, record: SSLCertificate) => {
        let tooltip = "";
        if (status === "self_signed") {
          tooltip = "Stop-gap self-signed cert — ACME will retry shortly";
        } else if (status === "pending_acme_retry") {
          tooltip = `ACME failed — retrying at ${formatDate(record.next_retry_at)}`;
        }
        return (
          <Tooltip title={tooltip}>
            <Tag
              color={STATUS_COLORS[status] || "default"}
              icon={STATUS_ICONS[status]}
            >
              {status.charAt(0).toUpperCase() + status.slice(1).replace(/_/g, " ")}
            </Tag>
          </Tooltip>
        );
      },
    },
    {
      title: "Last check",
      dataIndex: "last_attempt_at",
      key: "last_attempt_at",
      render: (dateStr: string | null) => {
        if (!dateStr) return "—";
        try {
          const date = new Date(dateStr);
          return date.toLocaleString();
        } catch {
          return "—";
        }
      },
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
      render: (dateStr: string | null, record: SSLCertificate) => {
        if (record.status === "self_signed") {
          return (
            <Typography.Text type="secondary">
              {formatDate(dateStr)} <em>(self-signed)</em>
            </Typography.Text>
          );
        }
        return formatExpiry(dateStr);
      },
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
      render: (_: unknown, record: SSLCertificate) => {
        const isRetryable = record.status === "failed" ||
          (record.status === "pending_acme_retry" && record.next_retry_at && new Date(record.next_retry_at) < new Date());
        return (
          <Space>
            {isRetryable && (
              <Tooltip title="Force ACME retry now">
                <Button
                  icon={<RedoOutlined />}
                  loading={retryMutation.isPending}
                  onClick={() => retryMutation.mutate(record.domain_id)}
                />
              </Tooltip>
            )}
            {record.status === "issued" && (
              <Tooltip title="Renew certificate">
                <Button
                  type="primary"
                  icon={<ReloadOutlined />}
                  loading={renewMutation.isPending}
                  onClick={() => renewMutation.mutate(record.domain_id)}
                />
              </Tooltip>
            )}
            {record.status === "issued" && (
              <Popconfirm
                title="Revoke Certificate"
                description="Are you sure you want to revoke this certificate?"
                onConfirm={() => revokeMutation.mutate(record.domain_id)}
                okText="Yes"
                cancelText="No"
              >
                <Button
                  danger
                  icon={<DeleteOutlined />}
                  loading={revokeMutation.isPending}
                />
              </Popconfirm>
            )}
          </Space>
        );
      },
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
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Input.Search
            placeholder={showOwner ? "Search by domain or owner" : "Search by domain"}
            allowClear
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            onSearch={(value) => setSearch(value.trim())}
            style={{ maxWidth: 360 }}
          />
          <Table
            dataSource={filteredData}
            columns={columns}
            rowKey="id"
            loading={isLoading}
            pagination={{ pageSize: 25, showSizeChanger: true }}
            scroll={{ x: "max-content" }}
          />
        </Space>
      )}
    </>
  );
};
