import { useState, useEffect } from "react";
import { useNavigate } from "react-router";
import { useTable } from "@refinedev/antd";
import { Button, Space, Table, Tag, Typography, Alert, Spin, Empty } from "antd";
import { ArrowLeftOutlined } from "@ant-design/icons";

import { apiClient } from "../../../apiClient";

interface Domain {
  id: string;
  user_id: string;
  name: string;
  created_at: string;
  updated_at: string;
}

interface ZoneStatus {
  provisioned: boolean;
}

export const DNSZonesOverviewPage = () => {
  const navigate = useNavigate();
  const [zoneStatuses, setZoneStatuses] = useState<Map<string, ZoneStatus>>(
    new Map()
  );

  const { tableProps, tableQueryResult } = useTable<Domain>({
    resource: "domains",
    syncWithLocation: true,
  });

  // Fetch zone status for each domain
  useEffect(() => {
    if (!tableQueryResult.data?.data) return;

    const domains = tableQueryResult.data.data;

    Promise.all(
      domains.map(async (domain) => {
        try {
          const res = await apiClient.get(`/domains/${domain.id}/dns/zone`);
          return {
            domainId: domain.id,
            provisioned: !!res.data?.data?.id,
          };
        } catch {
          // If zone doesn't exist or error, consider it not provisioned
          return {
            domainId: domain.id,
            provisioned: false,
          };
        }
      })
    ).then((results) => {
      const statusMap = new Map<string, ZoneStatus>();
      results.forEach(({ domainId, provisioned }) => {
        statusMap.set(domainId, { provisioned });
      });
      setZoneStatuses(statusMap);
    });
  }, [tableQueryResult.data?.data]);

  const getZoneStatusTag = (domainId: string) => {
    const status = zoneStatuses.get(domainId);
    if (status === undefined) {
      return <Spin size="small" />;
    }
    return status.provisioned ? (
      <Tag color="green">Provisioned</Tag>
    ) : (
      <Tag>Not provisioned</Tag>
    );
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
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          onClick={() => navigate("/jabali-admin/domains")}
        >
          Back to Domains
        </Button>
        <Typography.Title level={3} style={{ margin: 0 }}>
          DNS Zones
        </Typography.Title>
        <div />
      </Space>

      <Alert
        title="DNS zones are provisioned automatically when a domain is created. Nameservers are configured in Server Settings."
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />

      {tableQueryResult.isLoading ? (
        <Spin />
      ) : tableQueryResult.data?.data?.length === 0 ? (
        <Empty description="No domains found" />
      ) : (
        <Table<Domain> {...tableProps} rowKey="id">
          <Table.Column<Domain> dataIndex="name" title="Domain Name" />
          <Table.Column<Domain>
            dataIndex="user_id"
            title="Owner"
            render={(value: string) => value.substring(0, 8)}
          />
          <Table.Column<Domain>
            title="Zone Status"
            render={(_, record) => getZoneStatusTag(record.id)}
          />
          <Table.Column<Domain>
            title="Actions"
            render={(_, record) => (
              <Button
                type="primary"
                size="small"
                onClick={() => navigate(`/jabali-admin/domains/${record.id}/dns`)}
              >
                Manage Records
              </Button>
            )}
          />
        </Table>
      )}
    </div>
  );
};
