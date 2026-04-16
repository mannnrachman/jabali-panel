import { useState } from "react";
import { useTable } from "@refinedev/antd";
import { DeleteButton } from "@refinedev/antd";
import { PlusSquareOutlined, PauseCircleOutlined, PlayCircleOutlined } from "@ant-design/icons";
import { Button, Space, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";
import { useInvalidate, useNotification } from "@refinedev/core";
import { apiClient } from "../../../apiClient";

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  nginx_custom_directives: string;
  created_at: string;
  updated_at: string;
};

const DomainToggleButton = ({ domain }: { domain: Domain }) => {
  const [loading, setLoading] = useState(false);
  const invalidate = useInvalidate();
  const { open } = useNotification();

  const handleToggle = async () => {
    setLoading(true);
    try {
      await apiClient.patch(`/domains/${domain.id}`, {
        is_enabled: !domain.is_enabled,
      });
      open?.({
        type: "success",
        message: domain.is_enabled ? "Domain disabled" : "Domain enabled",
      });
      invalidate({ resource: "domains", invalidates: ["list"] });
    } catch (err) {
      open?.({
        type: "error",
        message: "Failed to toggle",
        description: (err as Error).message,
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <Button
      type="text"
      size="small"
      icon={domain.is_enabled ? <PauseCircleOutlined /> : <PlayCircleOutlined />}
      onClick={handleToggle}
      loading={loading}
    >
      {domain.is_enabled ? "Disable" : "Enable"}
    </Button>
  );
};

export const UserDomainList = () => {
  const navigate = useNavigate();
  const { tableProps } = useTable<Domain>({
    resource: "domains",
    syncWithLocation: true,
  });

  return (
    <div style={{ padding: 24 }}>
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
      >
        <Typography.Title level={3} style={{ margin: 0 }}>
          My Domains
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusSquareOutlined />}
          onClick={() => navigate("/jabali-panel/domains/create")}
        >
          Add Domain
        </Button>
      </Space>

      <Table<Domain> {...tableProps} rowKey="id" bordered>
        <Table.Column<Domain> dataIndex="name" title="Name" />
        <Table.Column<Domain> dataIndex="doc_root" title="Doc Root" />
        <Table.Column<Domain>
          dataIndex="is_enabled"
          title="Status"
          render={(enabled: boolean) =>
            enabled ? (
              <Tag color="green">active</Tag>
            ) : (
              <Tag>disabled</Tag>
            )
          }
        />
        <Table.Column<Domain>
          title="Actions"
          dataIndex="actions"
          render={(_, r) => (
            <Space size="small">
              <DomainToggleButton domain={r} />
              <DeleteButton hideText size="small" recordItemId={r.id} />
            </Space>
          )}
        />
      </Table>
    </div>
  );
};
