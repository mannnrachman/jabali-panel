// ChannelsTab — admin list of notification channels (M14 Step 6/9).
//
// Rendered inside NotificationsTabsPage Card.tabList. Strips its own
// page-level header; the "Add channel" button stays here because it's
// tab-specific (the History tab has a different action).
import { Button, Popconfirm, Space, Switch, Table, Tag, message } from "antd";
import { useState } from "react";

import { DeleteOutlined, EditOutlined, PlusOutlined, SendOutlined } from "@icons";

import { apiClient } from "../../../apiClient";
import { SearchableTableStringQ } from "../../../components/SearchableTable";
import {
  useDeleteMutation,
  useUpdateMutation,
} from "../../../hooks/useQueries";
import { useTableURL } from "../../../hooks/useTableURL";
import { AdminChannelDrawer, type NotificationChannel } from "./AdminChannelDrawer";
import { kindColors, kindLabels } from "./channelKindConfig";

const RESOURCE = "admin/notifications/channels";

export const ChannelsTab = () => {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<NotificationChannel | undefined>();

  const query = useTableURL<NotificationChannel>({
    resource: RESOURCE,
    defaultSort: "created_at",
    defaultOrder: "desc",
  });
  const updateMutation = useUpdateMutation<NotificationChannel, { enabled: boolean }>({ resource: RESOURCE });
  const deleteMutation = useDeleteMutation({ resource: RESOURCE });

  const handleToggleEnabled = async (row: NotificationChannel, next: boolean) => {
    try {
      await updateMutation.mutateAsync({ id: row.id, input: { enabled: next } });
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Toggle failed");
    }
  };

  const handleDelete = async (row: NotificationChannel) => {
    try {
      await deleteMutation.mutateAsync({ id: row.id });
      message.success(`Deleted ${row.name}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Delete failed");
    }
  };

  const handleTest = async (row: NotificationChannel) => {
    try {
      await apiClient.post(`/${RESOURCE}/${row.id}/test`);
      message.success(`Test envelope fired for ${row.name}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Test failed");
    }
  };

  const openCreate = () => {
    setEditing(undefined);
    setDrawerOpen(true);
  };

  const openEdit = (row: NotificationChannel) => {
    setEditing(row);
    setDrawerOpen(true);
  };

  return (
    <div>
      <Space style={{ marginBottom: 16, width: "100%", justifyContent: "flex-end" }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          Add channel
        </Button>
      </Space>

      <SearchableTableStringQ<NotificationChannel>
        rowKey="id"
        loading={query.isLoading}
        dataSource={query.items}
        initialSearch={query.params.q}
        searchPlaceholder="Search by name"
        onSearchChange={(q) => query.setParams({ q, page: 1 })}
        pagination={{
          current: query.params.page,
          pageSize: query.params.pageSize,
          total: query.total,
        }}
        scroll={{ x: "max-content" }}
      >
        <Table.Column
          dataIndex="name"
          title="Name"
          render={(name: string, row: NotificationChannel) => (
            <a onClick={() => openEdit(row)}>{name}</a>
          )}
        />
        <Table.Column
          dataIndex="kind"
          title="Kind"
          render={(k: string) => (
            <Tag color={kindColors[k as keyof typeof kindColors]}>
              {kindLabels[k as keyof typeof kindLabels] ?? k}
            </Tag>
          )}
        />
        <Table.Column
          dataIndex="enabled"
          title="Enabled"
          render={(enabled: boolean, row: NotificationChannel) => (
            <Switch checked={enabled} onChange={(next) => handleToggleEnabled(row, next)} />
          )}
        />
        <Table.Column
          title="Actions"
          key="actions"
          render={(_: unknown, row: NotificationChannel) => (
            <Space>
              <Button size="small" icon={<SendOutlined />} onClick={() => handleTest(row)}>
                Test
              </Button>
              <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(row)}>
                Edit
              </Button>
              <Popconfirm title={`Delete ${row.name}?`} onConfirm={() => handleDelete(row)}>
                <Button size="small" danger icon={<DeleteOutlined />}>
                  Delete
                </Button>
              </Popconfirm>
            </Space>
          )}
        />
      </SearchableTableStringQ>

      <AdminChannelDrawer open={drawerOpen} onClose={() => setDrawerOpen(false)} existing={editing} />
    </div>
  );
};
