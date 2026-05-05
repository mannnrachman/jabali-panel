import { useState } from "react";
import { Table, Button, Space, Select, message, Typography } from "antd";
import { EyeOutlined, PlayCircleOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../../../apiClient";
import { LogStreamModal } from "./LogStreamModal";

const { Text } = Typography;
const { Option } = Select;

interface Domain {
  id: string;
  name: string;
  status: string;
}

interface AccessLogTabProps {
  refreshTrigger: number;
}

export const AccessLogTab = ({ refreshTrigger }: AccessLogTabProps) => {
  const [selectedDomain, setSelectedDomain] = useState<string | undefined>(undefined);
  const [streamModalVisible, setStreamModalVisible] = useState(false);
  const [streamKey, setStreamKey] = useState<string | null>(null);
  const [streamUrl, setStreamUrl] = useState<string | null>(null);

  // Fetch available domains
  const { data: domainsData, isLoading: domainsLoading } = useQuery({
    queryKey: ["domains", refreshTrigger],
    queryFn: async () => {
      const response = await apiClient.get("/domains");
      return response.data;
    }
  });

  const domains: Domain[] = domainsData?.data || [];

  // Create access stream
  const createAccessStream = async (domainId?: string) => {
    try {
      const payload: any = {
        log_type: "access"
      };

      if (domainId) {
        payload.domain_id = domainId;
      }

      const response = await apiClient.post("/logs/access", payload);
      const { stream_key, websocket_url } = response.data;

      setStreamKey(stream_key);
      setStreamUrl(websocket_url);
      setStreamModalVisible(true);

      message.success("Access log stream created successfully");
    } catch (error: any) {
      message.error(error.response?.data?.error || "Failed to create log stream");
    }
  };

  const handleStreamClose = async () => {
    if (streamKey) {
      try {
        await apiClient.delete(`/logs/access/${streamKey}`);
        message.success("Log stream closed");
      } catch (error) {
        // Stream might have expired, ignore error
      }
      setStreamKey(null);
      setStreamUrl(null);
    }
    setStreamModalVisible(false);
  };

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
      )
    },
    {
      title: "Actions",
      key: "actions",
      render: (_: any, record: Domain) => (
        <Space size="middle">
          <Button
            type="text"
            icon={<EyeOutlined />}
            onClick={() => createAccessStream(record.id)}
          >
            View Logs
          </Button>
          <Button
            type="primary"
            icon={<PlayCircleOutlined />}
            onClick={() => createAccessStream(record.id)}
          >
            Stream Live
          </Button>
        </Space>
      )
    }
  ];

  // Add "All Domains" option for admin users
  const tableData = [
    { id: "", name: "All Domains", status: "system" },
    ...domains
  ];

  return (
    <div>
      <Space direction="vertical" size="large" style={{ width: "100%" }}>
        <div>
          <Space>
            <Select
              placeholder="Filter by domain"
              allowClear
              style={{ width: 200 }}
              value={selectedDomain}
              onChange={setSelectedDomain}
            >
              <Option value="">All Domains</Option>
              {domains.map(domain => (
                <Option key={domain.id} value={domain.id}>
                  {domain.name}
                </Option>
              ))}
            </Select>
            <Button
              type="primary"
              icon={<PlayCircleOutlined />}
              onClick={() => createAccessStream(selectedDomain)}
            >
              Stream Access Logs
            </Button>
          </Space>
        </div>

        <Table
          columns={columns}
          dataSource={tableData.filter(item =>
            !selectedDomain || selectedDomain === "" || item.id === selectedDomain
          )}
          rowKey="id"
          loading={domainsLoading}
          pagination={false}
          size="middle"
        />
      </Space>

      <LogStreamModal
        visible={streamModalVisible}
        onClose={handleStreamClose}
        streamUrl={streamUrl}
        title="Access Log Stream"
        logType="access"
      />
    </div>
  );
};