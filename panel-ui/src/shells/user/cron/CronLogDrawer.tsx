import { useState } from "react";
import {
  Drawer,
  Button,
  Space,
  Card,
  Select,
  Spin,
  message,
} from "antd";
import {
  CopyOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { getCronJobLog } from "../../../apiClient";

interface CronLogDrawerProps {
  open: boolean;
  onClose: () => void;
  jobId: string;
}

export const CronLogDrawer = ({
  open,
  onClose,
  jobId,
}: CronLogDrawerProps) => {
  const [lines, setLines] = useState<number>(200);

  const {
    data: logResponse = { log: "", lines: 0 },
    isLoading,
    refetch,
  } = useQuery({
    queryKey: ["cron-log", jobId, lines],
    queryFn: async () => getCronJobLog(jobId, lines),
    enabled: open,
  });

  const handleCopyToClipboard = () => {
    navigator.clipboard.writeText(logResponse.log).then(() => {
      message.success("Log copied to clipboard");
    });
  };

  const handleRefresh = () => {
    refetch();
  };

  return (
    <Drawer
      title="Cron Job Log"
      placement="right"
      onClose={onClose}
      open={open}
      width={700}
      extra={
        <Space>
          <Button
            type="text"
            size="small"
            icon={<CopyOutlined />}
            onClick={handleCopyToClipboard}
          >
            Copy
          </Button>
          <Button
            type="text"
            size="small"
            icon={<ReloadOutlined />}
            onClick={handleRefresh}
            loading={isLoading}
          >
            Refresh
          </Button>
        </Space>
      }
    >
      <Space orientation="vertical" style={{ width: "100%", marginBottom: 16 }}>
        <Select
          style={{ width: 120 }}
          value={lines}
          onChange={setLines}
          options={[
            { label: "Last 50 lines", value: 50 },
            { label: "Last 200 lines", value: 200 },
            { label: "Last 500 lines", value: 500 },
          ]}
        />
      </Space>

      <Spin spinning={isLoading}>
        <Card
          size="small"
          style={{
            backgroundColor: "#f5f5f5",
          }}
        >
          <pre
            style={{
              fontFamily: "monospace",
              fontSize: "12px",
              margin: 0,
              maxHeight: "600px",
              overflow: "auto",
              whiteSpace: "pre-wrap",
              wordWrap: "break-word",
            }}
          >
            {logResponse.log || "(no log content)"}
          </pre>
        </Card>
      </Spin>
    </Drawer>
  );
};
