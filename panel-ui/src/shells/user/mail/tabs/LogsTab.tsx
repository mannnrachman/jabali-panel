// LogsTab — M6.5 Step 7. Read-only mail log viewer.

import { useState } from "react";
import {
  Alert,
  
  DatePicker,
  Input,
  Space,
  Table,
  Typography,
} from "antd";
import { ReloadOutlined } from "@icons";
import type { Dayjs } from "dayjs";

import { useMailLogs } from "../../../../hooks/useMailLogs";

interface Filters {
  from?: Dayjs | null;
  to?: Dayjs | null;
  sender?: string;
  recipient?: string;
}

export const LogsTab = () => {
  const [filters, setFilters] = useState<Filters>({});
  const [page, setPage] = useState(1);
  const pageSize = 50;

  const { data, isLoading, error, refetch, isFetching } = useMailLogs({
    from_date: filters.from ? filters.from.toISOString() : undefined,
    to_date: filters.to ? filters.to.toISOString() : undefined,
    sender: filters.sender,
    recipient: filters.recipient,
    limit: pageSize,
    offset: (page - 1) * pageSize,
  });

  const entries = data?.data ?? [];
  const total = data?.total ?? 0;

  return (
    <div>
      <Space style={{ width: "100%", justifyContent: "space-between", marginBottom: 12, flexWrap: "wrap", rowGap: 8 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          Mail Logs
        </Typography.Title>
        <Space>
          <span
            role="button"
            onClick={() => refetch()}
            style={{ cursor: "pointer", color: "#1677ff", display: "inline-flex", alignItems: "center", gap: 4 }}
          >
            <ReloadOutlined spin={isFetching} /> Refresh
          </span>
        </Space>
      </Space>

      <Space wrap style={{ marginBottom: 12 }}>
        <DatePicker
          showTime
          placeholder="From"
          value={filters.from}
          onChange={(v) => setFilters((f) => ({ ...f, from: v }))}
        />
        <DatePicker
          showTime
          placeholder="To"
          value={filters.to}
          onChange={(v) => setFilters((f) => ({ ...f, to: v }))}
        />
        <Input
          placeholder="Sender contains"
          allowClear
          value={filters.sender ?? ""}
          onChange={(e) => setFilters((f) => ({ ...f, sender: e.target.value }))}
          style={{ width: 200 }}
        />
        <Input
          placeholder="Recipient contains"
          allowClear
          value={filters.recipient ?? ""}
          onChange={(e) => setFilters((f) => ({ ...f, recipient: e.target.value }))}
          style={{ width: 200 }}
        />
      </Space>

      {error && (
        <Alert
          type="warning"
          message="Mail logs unavailable"
          description="The mail server's trace log isn't responding. Try again in a moment."
          style={{ marginBottom: 12 }}
          showIcon
        />
      )}

      <Table
        rowKey={(r) => `${r.timestamp}-${r.from}-${r.to}`}
        dataSource={entries}
        loading={isLoading}
        pagination={{
          current: page,
          pageSize,
          total,
          showSizeChanger: false,
          onChange: (p) => setPage(p),
        }}
        scroll={{ x: "max-content" }}
        columns={[
          {
            title: "Timestamp",
            dataIndex: "timestamp",
            width: 200,
            render: (v: string) => new Date(v).toLocaleString(),
          },
          {
            title: "From",
            dataIndex: "from",
            render: (v: string) => (
              <Typography.Text style={{ fontFamily: "monospace" }}>{v}</Typography.Text>
            ),
          },
          {
            title: "To",
            dataIndex: "to",
            render: (v: string) => (
              <Typography.Text style={{ fontFamily: "monospace" }}>{v}</Typography.Text>
            ),
          },
          {
            title: "Size",
            dataIndex: "size",
            width: 100,
            render: (n: number) => `${(n / 1024).toFixed(1)} KB`,
          },
        ]}
      />
    </div>
  );
};
