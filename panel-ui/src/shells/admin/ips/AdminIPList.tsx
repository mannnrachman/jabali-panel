// AdminIPList — admin list of managed IPs (M24).
//
// Same shape as PackageList.tsx: useTableURL + SearchableTable +
// RowDeleteButton. Delete handles the 409 ip_in_use case by surfacing
// the affected-domains list returned by the API.
import { Button, Card, Modal, Space, Table, Tag, Typography, message } from "antd";
import { GlobalOutlined } from "@icons";
import { useState } from "react";

import { SearchableTableStringQ } from "../../../components/SearchableTable";
import { useDeleteMutation } from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";
import { AdminIPDrawer } from "./AdminIPDrawer";

type ManagedIP = {
  id: number;
  address: string;
  family: "ipv4" | "ipv6";
  label: string;
  is_default: boolean;
  is_bound: boolean;
  is_user_selectable: boolean;
  degraded: boolean;
  // kernel_present is populated from agent ip.list when the agent is
  // reachable; omitted when the probe fails (UI falls back to the
  // is_bound-only view).
  kernel_present?: boolean;
  created_at: string;
  updated_at: string;
};

const renderBoundTag = (row: ManagedIP) => {
  const { is_bound, kernel_present } = row;
  if (kernel_present === undefined) {
    return is_bound ? <Tag color="green">bound</Tag> : <Tag>unbound</Tag>;
  }
  if (is_bound && kernel_present) return <Tag color="green">bound</Tag>;
  if (is_bound && !kernel_present) return <Tag color="red">lost</Tag>;
  if (!is_bound && kernel_present) return <Tag color="blue">system</Tag>;
  return <Tag>unbound</Tag>;
};

// affectedDomainsError is the body shape the API returns on 409
// ip_in_use. We surface the list in a Modal so the admin can copy
// names and reassign before retrying the delete.
type AffectedDomainsBody = {
  error?: string;
  detail?: string;
  affected_domains?: string[];
  affected_count?: number;
};

export const AdminIPList = () => {
  const [conflictModal, setConflictModal] = useState<AffectedDomainsBody | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | undefined>(undefined);

  const openCreate = () => {
    setEditingId(undefined);
    setDrawerOpen(true);
  };
  const openEdit = (id: number) => {
    setEditingId(id);
    setDrawerOpen(true);
  };
  const closeDrawer = () => setDrawerOpen(false);

  const query = useTableURL<ManagedIP>({
    resource: "admin/ips",
    defaultSort: "id",
    defaultOrder: "asc",
  });
  const deleteMutation = useDeleteMutation({ resource: "admin/ips" });

  const handleDelete = async (row: ManagedIP) => {
    try {
      await deleteMutation.mutateAsync({ id: String(row.id) });
      message.success(`Removed ${row.address} from the pool`);
    } catch (err: unknown) {
      // Conflict surfaces the affected-domains list in a Modal — admin
      // needs to reassign those domains before deleting.
      const body = (err as { body?: AffectedDomainsBody })?.body;
      if (body?.error === "ip_in_use") {
        setConflictModal(body);
        return;
      }
      message.error(err instanceof Error ? err.message : "Delete failed");
    }
  };

  return (
    <div>
      <Space
        wrap
        align="center"
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          <GlobalOutlined /> IP Addresses
        </Typography.Title>
        <Button type="primary" onClick={openCreate}>
          Add IP
        </Button>
      </Space>

      <Card>
        <SearchableTableStringQ<ManagedIP>
          rowKey="id"
          loading={query.isLoading}
          dataSource={query.items}
          initialSearch={query.params.q}
          searchPlaceholder="Search by address or label"
          onSearchChange={(q) => query.setParams({ q, page: 1 })}
          pagination={{
            current: query.params.page,
            pageSize: query.params.pageSize,
            total: query.total,
          }}
        >
          <Table.Column
            dataIndex="address"
            title="Address"
            key="address"
            render={(addr: string) => <code>{addr}</code>}
          />
          <Table.Column
            dataIndex="family"
            title="Family"
            render={(family: ManagedIP["family"]) => (
              <Tag color={family === "ipv4" ? "blue" : "purple"}>{family}</Tag>
            )}
          />
          <Table.Column dataIndex="label" title="Label" />
          <Table.Column
            dataIndex="is_default"
            title="Default"
            render={(v: boolean) => (v ? <Tag color="gold">default</Tag> : null)}
          />
          <Table.Column
            title="Bound"
            key="is_bound"
            render={(_: unknown, row: ManagedIP) => renderBoundTag(row)}
          />
          <Table.Column
            dataIndex="is_user_selectable"
            title="User-selectable"
            render={(v: boolean) =>
              v ? <Tag color="cyan">yes</Tag> : <Tag>no</Tag>
            }
          />
          <Table.Column
            dataIndex="degraded"
            title="Status"
            render={(v: boolean) =>
              v ? <Tag color="red">degraded</Tag> : <Tag color="green">ok</Tag>
            }
          />
          <Table.Column
            title="Actions"
            dataIndex="actions"
            render={(_: unknown, r: ManagedIP) => (
              <Space>
                <Button type="text" onClick={() => openEdit(r.id)}>
                  Edit
                </Button>
                <Button
                  danger
                  type="text"
                  loading={deleteMutation.isPending}
                  onClick={() => handleDelete(r)}
                >
                  Delete
                </Button>
              </Space>
            )}
          />
        </SearchableTableStringQ>
      </Card>

      <AdminIPDrawer open={drawerOpen} onClose={closeDrawer} editingId={editingId} />

      <Modal
        title="IP is in use"
        open={conflictModal !== null}
        onCancel={() => setConflictModal(null)}
        onOk={() => setConflictModal(null)}
        cancelButtonProps={{ style: { display: "none" } }}
      >
        <p>
          {conflictModal?.detail ??
            "This IP is bound to one or more domains. Reassign them before deleting."}
        </p>
        {conflictModal?.affected_count ? (
          <p>
            <strong>{conflictModal.affected_count}</strong> domain
            {conflictModal.affected_count === 1 ? "" : "s"} reference this IP:
          </p>
        ) : null}
        {conflictModal?.affected_domains?.length ? (
          <ul style={{ maxHeight: 200, overflowY: "auto" }}>
            {conflictModal.affected_domains.map((d) => (
              <li key={d}>{d}</li>
            ))}
          </ul>
        ) : null}
      </Modal>
    </div>
  );
};
