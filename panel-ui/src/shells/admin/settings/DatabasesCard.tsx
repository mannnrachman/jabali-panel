// Databases tab — Server Settings card for opt-in PostgreSQL
// (M37 Phase 4). Mirrors the EmailCard / PanelSSLCard pattern: own
// data fetch, own apiClient calls, no coupling to the parent page's
// form. The parent's ServerSettings type also carries postgres_enabled
// so a global PATCH still round-trips that field — but the user-facing
// UX here is "flip switch → wait for the server to install/disable
// postgresql.service" with a status badge that polls `/admin/databases/
// postgres/status`.
//
// Toggle ON  → backend dispatches db.postgres.install (apt + initdb +
//              systemctl enable+start). Up to ~60 s on a fresh host.
// Toggle OFF → backend dispatches db.postgres.disable (stop+disable;
//              data on /var/lib/postgresql is preserved).
// Uninstall  → separate destructive button. apt purge + rm -rf data.
//              Two-step confirm dialog (you don't get this back).

import {
  DatabaseOutlined,
  ExclamationCircleOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import {
  App as AntdApp,
  Button,
  Card,
  Skeleton,
  Space,
  Switch,
  Tag,
  Typography,
} from "antd";
import { useEffect, useState } from "react";

import { apiClient } from "../../../apiClient";

interface PostgresStatus {
  installed: boolean;
  active: boolean;
  version?: string;
}

interface ServerSettingsPostgres {
  postgres_enabled: boolean;
}

export function DatabasesCard() {
  const { message, modal } = AntdApp.useApp();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [status, setStatus] = useState<PostgresStatus | null>(null);
  const [statusLoading, setStatusLoading] = useState(false);
  const [toggling, setToggling] = useState(false);
  const [uninstalling, setUninstalling] = useState(false);

  const refresh = async () => {
    setStatusLoading(true);
    try {
      const settings = await apiClient.get<ServerSettingsPostgres>(
        "/admin/settings",
      );
      setEnabled(settings.data.postgres_enabled);
      const st = await apiClient.get<PostgresStatus>(
        "/admin/databases/postgres/status",
      );
      setStatus(st.data);
    } catch (err) {
      message.error(
        `Could not load PostgreSQL status: ${
          err instanceof Error ? err.message : String(err)
        }`,
      );
    } finally {
      setStatusLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const handleToggle = async (next: boolean) => {
    setToggling(true);
    try {
      await apiClient.patch("/admin/settings", { postgres_enabled: next });
      message.success(
        next
          ? "Installing PostgreSQL — this can take up to a minute."
          : "Disabling PostgreSQL (data preserved).",
      );
      // Allow the agent dispatch to finish before re-polling.
      setTimeout(() => void refresh(), next ? 8000 : 2000);
      setEnabled(next);
    } catch (err) {
      message.error(
        `Toggle failed: ${err instanceof Error ? err.message : String(err)}`,
      );
    } finally {
      setToggling(false);
    }
  };

  const handleUninstall = () => {
    modal.confirm({
      title: "Uninstall PostgreSQL?",
      icon: <ExclamationCircleOutlined />,
      content:
        "This permanently removes the PostgreSQL packages AND deletes /var/lib/postgresql. All Postgres databases on this server will be lost. This cannot be undone.",
      okText: "Uninstall and delete data",
      okType: "danger",
      cancelText: "Cancel",
      onOk: async () => {
        setUninstalling(true);
        try {
          await apiClient.post("/admin/databases/postgres/uninstall", {});
          message.success("PostgreSQL uninstalled and data removed.");
          await refresh();
        } catch (err) {
          message.error(
            `Uninstall failed: ${
              err instanceof Error ? err.message : String(err)
            }`,
          );
        } finally {
          setUninstalling(false);
        }
      },
    });
  };

  const renderStatusBadge = () => {
    if (statusLoading || status == null) return <Tag>checking…</Tag>;
    if (!status.installed) return <Tag>Not installed</Tag>;
    if (status.active) return <Tag color="green">Running</Tag>;
    return <Tag color="orange">Installed (stopped)</Tag>;
  };

  return (
    <Card
      title={
        <Space>
          <DatabaseOutlined />
          Databases
        </Space>
      }
      extra={
        <Button
          type="text"
          icon={<ReloadOutlined />}
          onClick={() => void refresh()}
          loading={statusLoading}
        >
          Refresh
        </Button>
      }
      style={{ marginBottom: 16 }}
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        MariaDB is always installed. PostgreSQL is opt-in — enable below to
        install postgresql-16 on this server. Disabling stops the service
        without deleting data; uninstalling drops the data permanently.
      </Typography.Paragraph>

      {enabled == null ? (
        <Skeleton active paragraph={{ rows: 2 }} />
      ) : (
        <>
          <Space size="large" style={{ marginBottom: 12 }} wrap>
            <Space>
              <Switch
                checked={enabled}
                loading={toggling}
                onChange={handleToggle}
              />
              <Typography.Text strong>PostgreSQL</Typography.Text>
            </Space>
            {renderStatusBadge()}
            {status?.version && (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {status.version}
              </Typography.Text>
            )}
          </Space>

          {status?.installed && (
            <div>
              <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
                Removing PostgreSQL also drops every database created from
                the user databases page. Make sure backups are taken.
              </Typography.Paragraph>
              <Button danger onClick={handleUninstall} loading={uninstalling}>
                Uninstall PostgreSQL
              </Button>
            </div>
          )}
        </>
      )}
    </Card>
  );
}
