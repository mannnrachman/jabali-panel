// UserDNSZonesOverviewPage — tenant landing for DNS. Parallel of
// admin/dns/DNSZonesOverviewPage but navigates within /jabali-panel
// for the DNS-record deep-link.
import { useEffect, useState } from "react";
import { Alert, Button, Empty, Spin, Table, Tag, Typography } from "antd";

import { apiClient } from "../../../apiClient";
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

export const UserDNSZonesOverviewPage = () => {
  const [zoneStatuses, setZoneStatuses] = useState<Map<string, ZoneStatus>>(
    new Map(),
  );

  const query = useTableURL<Domain>({
    resource: "domains",
    defaultSort: "name",
    defaultOrder: "asc",
  });

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
          return { domainId: domain.id, provisioned: false };
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

      {query.isLoading ? (
        <Spin />
      ) : query.items.length === 0 ? (
        <Empty description="No domains found" />
      ) : (
        <Table<Domain>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          pagination={{
            current: query.params.page,
            pageSize: query.params.pageSize,
            total: query.total,
            onChange: (page, pageSize) => query.setParams({ page, pageSize }),
          }}
        >
          <Table.Column<Domain> dataIndex="name" title="Domain Name" />
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
                  navigate(`/jabali-panel/domains/${record.id}/dns`)
                }
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
