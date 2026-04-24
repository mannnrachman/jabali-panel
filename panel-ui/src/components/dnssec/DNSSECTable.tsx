// DNSSECTable — shared table for admin + user DNSSEC pages.
//
// Renders one row per owned domain with live DNSSEC state. The "Enable"
// column is a Switch that fires PUT /domains/:id/dnssec; the "Keys"
// column shows KSK/ZSK tags as Tags; the "View DS" action opens a
// modal with DS records (read-through via agent — never cached).
//
// The admin variant shows an "Owner" column by passing showOwner.
import { useMemo, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Empty,
  Modal,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";

import { ShieldCheckOutlined } from "@icons";

import { columnSearchProps } from "../columnSearch";
import { SearchableTableStringQ } from "../SearchableTable";
import { useTableURL } from "../../hooks/useTableURL";
import {
  algorithmLabel,
  digestTypeLabel,
  useDNSSECState,
  useDSRecords,
  useUpdateDNSSEC,
} from "../../hooks/useDNSSEC";

interface Domain {
  id: string;
  user_id: string;
  name: string;
  created_at: string;
  updated_at: string;
}

interface Props {
  showOwner: boolean;
}

export function DNSSECTable({ showOwner }: Props) {
  const [dsDomainID, setDsDomainID] = useState<string | null>(null);

  const query = useTableURL<Domain>({
    resource: "domains",
    defaultSort: "name",
    defaultOrder: "asc",
  });

  return (
    <>
      <Card>
        {query.isLoading ? (
          <Spin />
        ) : query.items.length === 0 ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No domains" />
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
              title="Domain"
              {...columnSearchProps<Domain>({
                placeholder: "Search by domain name",
                currentQ: query.params.q,
                onSearch: (v) => query.setParams({ q: v, page: 1 }),
              })}
            />
            {showOwner && (
              <Table.Column<Domain>
                dataIndex="user_id"
                title="Owner"
                render={(v: string) => (
                  <Typography.Text type="secondary" code>
                    {v.substring(0, 8)}
                  </Typography.Text>
                )}
              />
            )}
            <Table.Column<Domain>
              title="DNSSEC"
              render={(_, record) => <DNSSECStatus domainID={record.id} />}
            />
            <Table.Column<Domain>
              title="Keys"
              render={(_, record) => <DNSSECKeys domainID={record.id} />}
            />
            <Table.Column<Domain>
              title="Enable"
              render={(_, record) => <DNSSECToggle domainID={record.id} />}
            />
            <Table.Column<Domain>
              title="Actions"
              render={(_, record) => (
                <Button
                  size="small"
                  onClick={() => setDsDomainID(record.id)}
                >
                  View DS
                </Button>
              )}
            />
          </SearchableTableStringQ>
        )}
      </Card>

      <DSModal
        domainID={dsDomainID}
        open={dsDomainID !== null}
        onClose={() => setDsDomainID(null)}
      />
    </>
  );
}

function DNSSECStatus({ domainID }: { domainID: string }) {
  const { data, isLoading } = useDNSSECState(domainID);
  if (isLoading) return <Spin size="small" />;
  if (!data) return <Tag>unknown</Tag>;
  return data.enabled ? (
    <Tag color="green" icon={<ShieldCheckOutlined />}>
      Signed
    </Tag>
  ) : (
    <Tag>Unsigned</Tag>
  );
}

function DNSSECKeys({ domainID }: { domainID: string }) {
  const { data, isLoading } = useDNSSECState(domainID);
  if (isLoading) return <Spin size="small" />;
  if (!data || !data.enabled) return <Typography.Text type="secondary">—</Typography.Text>;
  if (data.keys.length === 0) {
    return <Typography.Text type="secondary">provisioning…</Typography.Text>;
  }
  return (
    <Space size={4} wrap>
      {data.keys.map((k) => (
        <Tooltip
          key={`${k.key_tag}-${k.key_type}`}
          title={`${k.key_type} · tag ${k.key_tag} · ${algorithmLabel(k.algorithm)}${k.active ? "" : " (pending)"}`}
        >
          <Tag color={k.key_type === "KSK" ? "blue" : "geekblue"}>
            {k.key_type} {k.key_tag}
          </Tag>
        </Tooltip>
      ))}
    </Space>
  );
}

function DNSSECToggle({ domainID }: { domainID: string }) {
  const { data, isLoading } = useDNSSECState(domainID);
  const mutation = useUpdateDNSSEC(domainID);
  const busy = isLoading || mutation.isPending;
  return (
    <Switch
      checked={!!data?.enabled}
      loading={busy}
      onChange={async (checked) => {
        try {
          await mutation.mutateAsync(checked);
          message.success(checked ? "DNSSEC enabled" : "DNSSEC disabled");
        } catch (err: unknown) {
          const msg = err instanceof Error ? err.message : "Failed";
          message.error(msg);
        }
      }}
    />
  );
}

function DSModal({
  domainID,
  open,
  onClose,
}: {
  domainID: string | null;
  open: boolean;
  onClose: () => void;
}) {
  const state = useDNSSECState(domainID ?? undefined);
  const enabled = !!state.data?.enabled;
  const ds = useDSRecords(domainID ?? undefined, open && enabled);

  const zoneName = state.data?.domain_name;

  const records = useMemo(() => ds.data?.ds_records ?? [], [ds.data]);

  return (
    <Modal
      title={zoneName ? `DS records · ${zoneName}` : "DS records"}
      open={open}
      onCancel={onClose}
      footer={[
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
      ]}
      width={720}
    >
      {!enabled ? (
        <Alert
          type="warning"
          showIcon
          message="DNSSEC is not enabled for this domain."
          description="Enable DNSSEC first, then come back here for DS records to publish at your registrar."
        />
      ) : ds.isLoading ? (
        <Spin />
      ) : ds.isError ? (
        <Alert
          type="error"
          showIcon
          message="Failed to fetch DS records"
          description={(ds.error as Error)?.message ?? "Unknown error"}
        />
      ) : records.length === 0 ? (
        <Empty description="No DS records — key may still be provisioning" />
      ) : (
        <>
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 16 }}
            message="Publish these DS records at your registrar"
            description="The registrar typically needs key tag, algorithm, digest type, and digest. Until the DS is in the parent zone, validators will not trust your signed data."
          />
          <Table
            rowKey={(r) => `${r.key_tag}-${r.digest_type}`}
            dataSource={records}
            pagination={false}
            scroll={{ x: "max-content" }}
            columns={[
              { title: "Key tag", dataIndex: "key_tag" },
              {
                title: "Algorithm",
                dataIndex: "algorithm",
                render: (v: number) => `${v} (${algorithmLabel(v)})`,
              },
              {
                title: "Digest type",
                dataIndex: "digest_type",
                render: (v: number) => `${v} (${digestTypeLabel(v)})`,
              },
              {
                title: "Digest",
                dataIndex: "digest",
                render: (v: string) => (
                  <Typography.Text code copyable={{ text: v }}>
                    {v}
                  </Typography.Text>
                ),
              },
            ]}
          />
        </>
      )}
    </Modal>
  );
}
