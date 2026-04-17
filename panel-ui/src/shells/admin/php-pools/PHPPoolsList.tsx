import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  notification,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from "antd";
import {
  CheckCircleOutlined,
  CodeOutlined,
  DownloadOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { apiClient } from "../../../apiClient";

interface PHPVersionStatus {
  version: string;
  installed: boolean;
  fpm_running: boolean;
}

interface PHPVersionStatusResponse {
  default_version: string;
  versions: PHPVersionStatus[];
}

interface PHPVersionAction {
  version: string;
  installed: boolean;
  fpm_running: boolean;
}

export const PHPPoolsList = () => {
  const [statusData, setStatusData] = useState<PHPVersionStatusResponse | null>(
    null
  );
  const [loading, setLoading] = useState(true);
  const [installingVersion, setInstallingVersion] = useState<string | null>(
    null
  );
  const [reloadingVersion, setReloadingVersion] = useState<string | null>(null);
  const [dismissedWarning, setDismissedWarning] = useState(false);

  useEffect(() => {
    fetchStatus();
  }, []);

  const fetchStatus = async () => {
    setLoading(true);
    try {
      const response = await apiClient.get<PHPVersionStatusResponse>(
        "/admin/php/versions/status"
      );
      setStatusData(response.data);
    } catch (error) {
      notification.error({
        message: "Failed to fetch PHP versions",
        description:
          error instanceof Error ? error.message : "Unknown error occurred",
        duration: 5,
      });
    } finally {
      setLoading(false);
    }
  };

  const handleInstall = async (version: string) => {
    setInstallingVersion(version);
    try {
      const response = await apiClient.post<PHPVersionAction>(
        `/admin/php/versions/${version}/install`
      );
      notification.success({
        message: `PHP ${version} installed successfully`,
        duration: 3,
      });
      setStatusData((prev) => {
        if (!prev) return null;
        return {
          ...prev,
          versions: prev.versions.map((v) =>
            v.version === version
              ? {
                  ...v,
                  installed: response.data.installed,
                  fpm_running: response.data.fpm_running,
                }
              : v
          ),
        };
      });
    } catch (error) {
      const errorMsg =
        error instanceof Error ? error.message : "Installation failed";
      notification.error({
        message: `Failed to install PHP ${version}`,
        description: errorMsg,
        duration: 5,
      });
    } finally {
      setInstallingVersion(null);
    }
  };

  const handleReload = async (version: string) => {
    setReloadingVersion(version);
    try {
      await apiClient.post(`/admin/php/versions/${version}/reload`);
      notification.success({
        message: `PHP ${version} reloaded successfully`,
        duration: 3,
      });
    } catch (error) {
      const errorMsg =
        error instanceof Error ? error.message : "Reload failed";
      notification.error({
        message: `Failed to reload PHP ${version}`,
        description: errorMsg,
        duration: 5,
      });
    } finally {
      setReloadingVersion(null);
    }
  };

  if (loading && !statusData) {
    return (
      <div style={{ padding: 24, textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }

  const tableData = statusData?.versions || [];

  return (
    <div style={{ padding: 24 }}>
      <Space
        style={{
          marginBottom: 16,
          width: "100%",
          justifyContent: "space-between",
        }}
        direction="vertical"
      >
        <div>
          <Typography.Title level={3} style={{ margin: "0 0 8px 0" }}>
            PHP Versions
          </Typography.Title>
          <Typography.Paragraph style={{ margin: 0 }}>
            Install, manage and configure PHP versions
          </Typography.Paragraph>
        </div>

        {!dismissedWarning && (
          <Alert
            type="warning"
            showIcon
            message="Modifying PHP versions can cause server downtime"
            description="Uninstalling PHP versions may break websites that depend on them. Ensure you understand the impact before making changes."
            closable
            onClose={() => setDismissedWarning(true)}
            style={{ marginBottom: 16 }}
          />
        )}
      </Space>

      <Table<PHPVersionStatus>
        dataSource={tableData}
        rowKey="version"
        bordered
        loading={loading}
        pagination={false}
      >
        <Table.Column<PHPVersionStatus>
          dataIndex="version"
          title="PHP Version"
          render={(version: string) => (
            <Space>
              <CodeOutlined />
              <strong>PHP {version}</strong>
            </Space>
          )}
        />
        <Table.Column<PHPVersionStatus>
          dataIndex="installed"
          title="Status"
          width={150}
          render={(installed: boolean) =>
            installed ? (
              <Tag color="green">Installed</Tag>
            ) : (
              <Tag>Not Installed</Tag>
            )
          }
        />
        <Table.Column<PHPVersionStatus>
          title="Default"
          width={100}
          render={(_: any, record: PHPVersionStatus) =>
            record.version === statusData?.default_version && record.installed ? (
              <CheckCircleOutlined style={{ color: "#1890ff", fontSize: 16 }} />
            ) : (
              "—"
            )
          }
        />
        <Table.Column<PHPVersionStatus>
          dataIndex="fpm_running"
          title="FPM"
          width={150}
          render={(fpmRunning: boolean, record: PHPVersionStatus) =>
            record.installed ? (
              fpmRunning ? (
                <Tag color="green">Running</Tag>
              ) : (
                <Tag>Stopped</Tag>
              )
            ) : (
              "—"
            )
          }
        />
        <Table.Column<PHPVersionStatus>
          title="Actions"
          width={120}
          render={(_: any, record: PHPVersionStatus) => {
            const isInstalling = installingVersion === record.version;
            const isReloading = reloadingVersion === record.version;

            if (record.installed) {
              return (
                <Button
                  type="link"
                  icon={<ReloadOutlined />}
                  onClick={() => handleReload(record.version)}
                  loading={isReloading}
                  disabled={isReloading}
                >
                  Reload
                </Button>
              );
            } else {
              return (
                <Button
                  type="link"
                  icon={<DownloadOutlined />}
                  onClick={() => handleInstall(record.version)}
                  loading={isInstalling}
                  disabled={isInstalling}
                >
                  Install
                </Button>
              );
            }
          }}
        />
      </Table>
    </div>
  );
};
