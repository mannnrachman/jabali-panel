import { useState } from "react";
import { Card, Typography, Button, Space, Tabs } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { UserAccessLogTab } from "./UserAccessLogTab";
import { UserErrorLogTab } from "./UserErrorLogTab";
import { UserRealtimeLogTab } from "./UserRealtimeLogTab";

const { Title } = Typography;

export const UserLogsPage = () => {
  const [activeTab, setActiveTab] = useState("access");
  const [refreshTrigger, setRefreshTrigger] = useState(0);

  const handleRefresh = () => {
    setRefreshTrigger(prev => prev + 1);
  };

  const tabItems = [
    {
      key: "access",
      label: "Access Log",
      children: <UserAccessLogTab refreshTrigger={refreshTrigger} />
    },
    {
      key: "error",
      label: "Error Log",
      children: <UserErrorLogTab refreshTrigger={refreshTrigger} />
    },
    {
      key: "realtime",
      label: "Real Time Log",
      children: <UserRealtimeLogTab refreshTrigger={refreshTrigger} />
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