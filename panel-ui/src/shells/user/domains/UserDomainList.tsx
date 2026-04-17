import { useTable } from "@refinedev/antd";
import { DeleteButton } from "@refinedev/antd";
import { SearchableTable } from "../../../components/SearchableTable";
import { PlusSquareOutlined, GlobalOutlined } from "@ant-design/icons";
import { Button, Space, Table, Tag, Typography } from "antd";
import { useNavigate } from "react-router";

import { DomainToggleButton } from "../../DomainToggleButton";
import { DomainSettingsButton } from "../../DomainSettingsButton";
import { DomainRedirectsButton } from "../../DomainRedirectsButton";
import { DomainIndexButton } from "../../DomainIndexButton";

export type Domain = {
  id: string;
  user_id: string;
  name: string;
  doc_root: string;
  is_enabled: boolean;
  nginx_custom_directives: string;
  redirect_all_to?: string | null;
  redirect_all_type?: string | null;
  page_redirects?: { source: string; destination: string; type: "301" | "302" | "307" | "308" }[] | null;
  index_priority?: "html_first" | "php_first" | "html_only" | "php_only" | "full" | null;
  created_at: string;
  updated_at: string;
};

export const UserDomainList = () => {
  const navigate = useNavigate();
  const { tableProps, setFilters } = useTable<Domain>({
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

      <SearchableTable<Domain>
        {...tableProps}
        rowKey="id"
        bordered
        searchPlaceholder="Search by domain name"
        onSearchChange={(filters) => setFilters(filters, "replace")}
      >
        <Table.Column<Domain> dataIndex="name" title="Name" sorter={{ multiple: 1 }} defaultSortOrder="ascend" />
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
              <Button
                type="text"
                size="small"
                icon={<GlobalOutlined />}
                onClick={() => navigate(`/jabali-panel/domains/${r.id}/dns`)}
              >
                DNS
              </Button>
              <DomainRedirectsButton domain={r} />
              <DomainIndexButton domain={r} />
              <DomainSettingsButton domain={r} />
              <DomainToggleButton domain={r} />
              <DeleteButton hideText size="small" type="text" resource="domains" recordItemId={r.id} />
            </Space>
          )}
        />
      </SearchableTable>
    </div>
  );
};
