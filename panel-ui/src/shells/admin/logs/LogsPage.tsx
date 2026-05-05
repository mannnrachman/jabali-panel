import { useState } from "react";
import { Card, Typography, Button, Space, Tabs } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { AccessLogTab } from "./AccessLogTab";
import { ErrorLogTab } from "./ErrorLogTab";
import { RealtimeLogTab } from "./RealtimeLogTab";

const { Title } = Typography;

export const LogsPage = () => {
  const [activeTab, setActiveTab] = useState("access");
  const [refreshTrigger, setRefreshTrigger] = useState(0);

  const handleRefresh = () => {
    setRefreshTrigger(prev => prev + 1);
  };

  const tabItems = [
    {
      key: "access",
      label: "Access Log",
      children: <AccessLogTab refreshTrigger={refreshTrigger} />
    },
    {
      key: "error",
      label: "Error Log",
      children: <ErrorLogTab refreshTrigger={refreshTrigger} />
    },
    {
      key: "realtime",
      label: "Real Time Log",
      children: <RealtimeLogTab refreshTrigger={refreshTrigger} />
    }
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
          onClick={handleRefresh}
        >
          Refresh
        </Button>
      </Space>

      <Card>
        <Tabs
          activeKey={activeTab}
          onChange={setActiveTab}
          items={tabItems}
          size="large"
        />
      </Card>
    </div>
  );
};