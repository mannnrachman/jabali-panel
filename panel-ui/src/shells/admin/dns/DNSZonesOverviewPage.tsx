// DNSZonesOverviewPage — admin landing for DNS. Lists every domain
// with a "zone provisioned?" badge so operators can see at a glance
// which zones didn't come up. Post-M21: useTable → useTableURL; the
// per-domain zone probe is unchanged (apiClient.get on each row
// after the list resolves).
import { useEffect, useState } from "react";
import { Alert, Button, Card, Empty, Spin, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";

import { apiClient } from "../../../apiClient";
import { columnSearchProps } from "../../../components/columnSearch";
import { useTableURL } from "../../../hooks/useTableURL";

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
    new Map(),
  );

  const query = useTableURL<Domain>({
    resource: "domains",
    defaultSort: "name",
    defaultOrder: "asc",
  });

  // Fetch zone status for each domain after the list resolves.
  // Keep this lightweight — one GET per row. Panel-side zones are
  // cheap to look up and most admin-scale installs have <100 domains.
  useEffect(() => {
    const domains = query.items;
    if (domains.length === 0) return;

    Promise.all(
      domains.map(async (domain) => {
        try {
          const res = await apiClient.get(`/domains/${domain.id}/dns/zone`);
          return {
            domainId: domain.id,
            provisioned: !!res.data?.data?.id,
          };
        } catch {
          // If zone doesn't exist or error, consider it not provisioned.
          return {
            domainId: domain.id,
            provisioned: false,
          };
        }
      }),
    ).then((results) => {
      const statusMap = new Map<string, ZoneStatus>();
      results.forEach(({ domainId, provisioned }) => {
        statusMap.set(domainId, { provisioned });
      });
      setZoneStatuses(statusMap);
    });
  }, [query.items]);

  const getZoneStatusTag = (domainId: string) => {
    const status = zoneStatuses.get(domainId);
    if (status === undefined) {
      return <Spin />;
    }
    return status.provisioned ? (
      <Tag color="green">Provisioned</Tag>
    ) : (
      <Tag>Not provisioned</Tag>
    );
  };

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        DNS Zones
      </Typography.Title>

      <Alert
        title="DNS zones are provisioned automatically when a domain is created. Nameservers are configured in Server Settings."
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />

      <Card>
        {query.isLoading ? (
          <Spin />
        ) : query.items.length === 0 ? (
          <Empty description="No domains found" />
        ) : (
          <Table<Domain>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          scroll={{ x: "max-content" }}
          pagination={{
            current: query.params.page,
            pageSize: query.params.pageSize,
            total: query.total,
            onChange: (page, pageSize) => query.setParams({ page, pageSize }),
          }}
        >
          <Table.Column<Domain>
            dataIndex="name"
            title="Domain Name"
            {...columnSearchProps<Domain>({
              placeholder: "Search by domain name",
              currentQ: query.params.q,
              onSearch: (v) => query.setParams({ q: v, page: 1 }),
            })}
          />
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
                onClick={() =>
                  navigate(`/jabali-admin/domains/${record.id}/dns`)
                }
              >
                Manage Records
              </Button>
            )}
          />
          </Table>
        )}
      </Card>
    </div>
  );
};
