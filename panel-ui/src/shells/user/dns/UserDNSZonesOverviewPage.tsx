// UserDNSZonesOverviewPage — tenant landing for DNS. Card.tabList
// pattern matches admin DNS + UserList — controlled activeTabKey,
// count Tag in each tab label, panel-attached strip. Both tabs view
// the same `domains` list.
import { useEffect, useState } from "react";
import { Alert, Button, Card, Empty, Spin, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";

import { apiClient } from "../../../apiClient";
import { columnSearchProps } from "../../../components/columnSearch";
import { DNSSECTable } from "../../../components/dnssec/DNSSECTable";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
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

const ZonesTab = () => {
  const navigate = useNavigate();
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
    <>
      <Alert
        title="DNS zones are provisioned automatically when a domain is created. Nameservers are configured in Server Settings."
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
      />
      {query.isLoading ? (
        <Spin />
      ) : query.items.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No domains found" />
      ) : (
        <SearchableTableStringQ<Domain>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          initialSearch={query.params.q}
          searchPlaceholder="Search by domain name"
          onSearchChange={(q) => query.setParams({ q, page: 1 })}
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
        </SearchableTableStringQ>
      )}
    </>
  );
};

const DNSSECTab = () => (
  <>
    <Alert
      type="info"
      showIcon
      style={{ marginBottom: 16 }}
      message="Protect your domain with DNSSEC."
      description="Enable signing here, then copy the DS record to your registrar to complete the chain of trust."
    />
    <DNSSECTable showOwner={false} />
  </>
);

export const UserDNSZonesOverviewPage = () => {
  const [activeTab, setActiveTab] = useState<"zones" | "dnssec">("zones");
  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        DNS
      </Typography.Title>
      <Card
        tabList={[
          { key: "zones", tab: "Zones" },
          { key: "dnssec", tab: "DNSSEC" },
        ]}
        activeTabKey={activeTab}
        onTabChange={(k) => setActiveTab(k as "zones" | "dnssec")}
      >
        {activeTab === "zones" ? <ZonesTab /> : <DNSSECTab />}
      </Card>
    </div>
  );
};
