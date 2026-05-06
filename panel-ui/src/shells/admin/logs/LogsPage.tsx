import { useState } from "react";
import { Card, Typography, Button, Space, Table, message } from "antd";
import {
  ReloadOutlined,
  FileTextOutlined,
  WarningOutlined,
  DashboardOutlined,
} from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../../../apiClient";
import { LogStreamModal } from "./LogStreamModal";

const { Title, Text } = Typography;

interface Domain {
  id: string;
  name: string;
  status: string;
}

type LogType = "access" | "error" | "goaccess";

const titleFor: Record<LogType, string> = {
  access: "Access Log Stream",
  error: "Error Log Stream",
  goaccess: "GoAccess Real-Time Dashboard",
};

const labelFor: Record<LogType, string> = {
  access: "Access Log",
  error: "Error Log",
  goaccess: "Real Time",
};

export const LogsPage = () => {
  const [refreshTrigger, setRefreshTrigger] = useState(0);
  const [streamKey, setStreamKey] = useState<string | null>(null);
  const [streamUrl, setStreamUrl] = useState<string | null>(null);
  const [streamLogType, setStreamLogType] = useState<LogType>("access");
  const [modalVisible, setModalVisible] = useState(false);

  const { data: domainsData, isLoading } = useQuery({
    queryKey: ["domains", refreshTrigger],
    queryFn: async () => {
      const response = await apiClient.get("/domains");
      return response.data;
    },
  });

  const domains: Domain[] = domainsData?.data || [];

  const openStream = async (logType: LogType, domainId?: string) => {
    try {
      const payload: { log_type: LogType; domain_id?: string } = { log_type: logType };
      if (domainId) payload.domain_id = domainId;

      const response = await apiClient.post("/logs/access", payload);
      const { stream_key, websocket_url } = response.data;

      setStreamKey(stream_key);
      setStreamUrl(websocket_url);
      setStreamLogType(logType);
      setModalVisible(true);
    } catch (error: unknown) {
      const msg =
        error && typeof error === "object" && "response" in error
          ? // @ts-expect-error axios error shape
            error.response?.data?.error
          : undefined;
      message.error(msg || "Failed to create log stream");
    }
  };

  const handleStreamClose = async () => {
    if (streamKey) {
      try {
        await apiClient.delete(`/logs/access/${streamKey}`);
      } catch {
        // Stream may have expired server-side; harmless.
      }
      setStreamKey(null);
      setStreamUrl(null);
    }
    setModalVisible(false);
  };

  const tableData: Domain[] = [
    { id: "", name: "All Domains", status: "system" },
    ...domains,
  ];

  const columns = [
    {
      title: "Domain",
      dataIndex: "name",
      key: "name",
      render: (name: string, record: Domain) => (
        <Space>
          <Text strong>{name}</Text>
          <Text type="secondary">({record.status})</Text>
        </Space>
      ),
    },
    {
      title: "Actions",
      key: "actions",
      render: (_: unknown, record: Domain) => (
        <Space size="small" wrap>
          <Button
            icon={<FileTextOutlined />}
            onClick={() => openStream("access", record.id || undefined)}
          >
            {labelFor.access}
          </Button>
          <Button
            icon={<WarningOutlined />}
            onClick={() => openStream("error", record.id || undefined)}
          >
            {labelFor.error}
          </Button>
          <Button
            type="primary"
            icon={<DashboardOutlined />}
            onClick={() => openStream("goaccess", record.id || undefined)}
          >
            {labelFor.goaccess}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 16, width: "100%", justifyContent: "space-between" }}>
        <Title level={2} style={{ margin: 0 }}>
          Logs & Statistics
        </Title>
        <Button
          type="primary"
          icon={<ReloadOutlined />}
          onClick={() => setRefreshTrigger((p) => p + 1)}
        >
          Refresh
        </Button>
      </Space>

      <Card>
        <Table
          columns={columns}
          dataSource={tableData}
          rowKey="id"
          loading={isLoading}
          pagination={false}
          size="middle"
          scroll={{ x: "max-content" }}
        />
      </Card>

      <LogStreamModal
        visible={modalVisible}
        onClose={handleStreamClose}
        streamUrl={streamUrl}
        title={titleFor[streamLogType]}
        logType={streamLogType}
      />
    </div>
  );
};
