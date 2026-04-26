// DNSZonesOverviewPage — admin landing for DNS. Tabs split the page
// into "Zones" (per-domain provisioning status) and "DNSSEC" (signing
// state + DS export). Both tabs share the URL-backed table params via
// useTableURL on `domains`, so a search in one tab carries over to
// the other — they're both views of the same domain list.
import { useEffect, useState } from "react";
import { Alert, Button, Card, Empty, Spin, Table, Tabs, Tag, Typography } from "antd";
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
      <Card>
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
      </Card>
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

export const DNSZonesOverviewPage = () => (
  <div>
    <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 16 }}>
      DNS
    </Typography.Title>
    <Tabs
      destroyOnHidden
      items={[
        { key: "zones", label: "Zones", children: <ZonesTab /> },
        { key: "dnssec", label: "DNSSEC", children: <DNSSECTab /> },
      ]}
    />
  </div>
);
