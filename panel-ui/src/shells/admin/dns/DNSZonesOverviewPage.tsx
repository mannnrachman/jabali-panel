// DNSZonesOverviewPage — admin landing for DNS. Card.tabList splits
// into "Zones" (per-domain provisioning) and "DNSSEC" (signing state
// + DS export). Mirrors the UserList tab style — a count Tag in each
// label, controlled activeTabKey, panel-attached strip. Both tabs
// view the same `domains` list so the badge total matches on both.
import { useEffect, useState } from "react";
import { Alert, Button, Card, Empty, Space, Spin, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";

import { apiClient } from "../../../apiClient";
import { columnSearchProps } from "../../../components/columnSearch";
import { DNSSECTable } from "../../../components/dnssec/DNSSECTable";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useListQuery } from "../../../hooks/useQueries";
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
      message="Sign zones with DNSSEC. Enable per-domain, then publish the DS record at the registrar."
      description="Signing is best-effort NSEC3 with ECDSAP256SHA256 (RFC 8624). Keys are managed by PowerDNS via pdnsutil."
    />
    <DNSSECTable showOwner />
  </>
);

export const DNSZonesOverviewPage = () => {
  const [activeTab, setActiveTab] = useState<"zones" | "dnssec">("zones");

  // pageSize=1 totals — both tabs view the same domain list, so the
  // badge mirrors the table pagination total without re-implementing
  // a count endpoint.
  const domainsCountQ = useListQuery<Domain>({
    resource: "domains",
    params: { pageSize: 1 },
  });

  return (
    <div>
      <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
        DNS
      </Typography.Title>
      <Card
        tabList={[
          {
            key: "zones",
            tab: (
              <Space>
                Zones
                <Tag>{domainsCountQ.total}</Tag>
              </Space>
            ),
          },
          {
            key: "dnssec",
            tab: (
              <Space>
                DNSSEC
                <Tag>{domainsCountQ.total}</Tag>
              </Space>
            ),
          },
        ]}
        activeTabKey={activeTab}
        onTabChange={(k) => setActiveTab(k as "zones" | "dnssec")}
      >
        {activeTab === "zones" ? <ZonesTab /> : <DNSSECTab />}
      </Card>
    </div>
  );
};
