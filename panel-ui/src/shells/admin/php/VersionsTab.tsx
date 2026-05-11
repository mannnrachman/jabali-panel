import { useEffect, useState } from "react";
import { Alert, notification, Popconfirm, Space, Spin, Table, Tag } from "antd";
import {
  CheckCircleOutlined,
  DeleteOutlined,
  DownloadOutlined,
  ReloadOutlined,
} from "@icons";
import { RowActionButton } from "../../../components/RowActionButton";
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

export const VersionsTab = () => {
  const [statusData, setStatusData] = useState<PHPVersionStatusResponse | null>(
    null
  );
  const [loading, setLoading] = useState(true);
  const [installingVersion, setInstallingVersion] = useState<string | null>(
    null
  );
  const [reloadingVersion, setReloadingVersion] = useState<string | null>(null);
  const [uninstallingVersion, setUninstallingVersion] = useState<string | null>(null);
  const [settingDefaultVersion, setSettingDefaultVersion] = useState<string | null>(null);
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
      // apt install of phpX.Y-fpm + extensions takes 30-90s. The
      // default 15s axios ceiling fires a "timeout exceeded" error
      // toast even when the install succeeds in the background. Bump
      // per-request to 5 min — matches backend adminActionTimeout in
      // panel-api/internal/api/php_versions.go.
      const response = await apiClient.post<PHPVersionAction>(
        `/admin/php/versions/${version}/install`,
        undefined,
        { timeout: 5 * 60 * 1000 },
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

  const handleUninstall = async (version: string) => {
    setUninstallingVersion(version);
    try {
      // apt-get purge --auto-remove on php<v>-* can take 30-120s on a
      // host with many extensions. Match install's 5-min ceiling.
      await apiClient.delete(`/admin/php/versions/${version}`, {
        timeout: 5 * 60 * 1000,
      });
      notification.success({
        message: `PHP ${version} uninstalled`,
        duration: 3,
      });
      await fetchStatus();
    } catch (error) {
      const errorMsg =
        error instanceof Error ? error.message : "Uninstall failed";
      notification.error({
        message: `Failed to uninstall PHP ${version}`,
        description: errorMsg,
        duration: 5,
      });
    } finally {
      setUninstallingVersion(null);
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

  const handleSetDefault = async (version: string) => {
    setSettingDefaultVersion(version);
    try {
      await apiClient.post(`/admin/php/versions/${version}/default`);
      notification.success({
        message: `PHP ${version} is now the default`,
        duration: 3,
      });
      setStatusData((prev) =>
        prev ? { ...prev, default_version: version } : prev,
      );
    } catch (error) {
      const errorMsg =
        error instanceof Error ? error.message : "Request failed";
      notification.error({
        message: `Could not set PHP ${version} as default`,
        description: errorMsg,
        duration: 5,
      });
    } finally {
      setSettingDefaultVersion(null);
    }
  };

  if (loading && !statusData) {
    return (
      <div style={{ textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }

  // Show newest-first (8.5 at top, 7.4 at bottom). Semver-aware sort:
  // split on "." and compare numerically so 8.10 > 8.9 if that ever matters.
  const tableData = [...(statusData?.versions || [])].sort((a, b) => {
    const pa = a.version.split(".").map(Number);
    const pb = b.version.split(".").map(Number);
    for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
      const diff = (pb[i] ?? 0) - (pa[i] ?? 0);
      if (diff !== 0) return diff;
    }
    return 0;
  });

  return (
    <>
      {!dismissedWarning && (
        <Alert
          type="warning"
          showIcon
          title="Modifying PHP versions can cause server downtime"
          description="Uninstalling PHP versions may break websites that depend on them. Ensure you understand the impact before making changes."
          closable
          onClose={() => setDismissedWarning(true)}
          style={{ marginBottom: 16 }}
        />
      )}

      <Table<PHPVersionStatus>
        dataSource={tableData}
        rowKey="version"
        loading={loading}
        pagination={false}
        scroll={{ x: "max-content" }}
      >
        <Table.Column<PHPVersionStatus>
          dataIndex="version"
          title="PHP Version"
          render={(version: string) => `PHP ${version}`}
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
          width={140}
          render={(_: any, record: PHPVersionStatus) => {
            if (record.version === statusData?.default_version && record.installed) {
              return <CheckCircleOutlined />;
            }
            // Button only shows for installed + FPM-running versions — the
            // API rejects setting a default that isn't both. Avoid the
            // failed-request UX entirely by hiding the button.
            if (record.installed && record.fpm_running) {
              return (
                <RowActionButton
                  icon={<CheckCircleOutlined />}
                  loading={settingDefaultVersion === record.version}
                  onClick={() => handleSetDefault(record.version)}
                >
                  Set default
                </RowActionButton>
              );
            }
            return "—";
          }}
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
          width={220}
          render={(_: any, record: PHPVersionStatus) => {
            const isInstalling = installingVersion === record.version;
            const isReloading = reloadingVersion === record.version;
            const isUninstalling = uninstallingVersion === record.version;
            const isDefault = statusData?.default_version === record.version;

            if (record.installed) {
              return (
                <Space size="small" wrap>
                  <RowActionButton
                    icon={<ReloadOutlined />}
                    onClick={() => handleReload(record.version)}
                    loading={isReloading}
                    disabled={isReloading || isUninstalling}
                  >
                    Reload
                  </RowActionButton>
                  {/* Uninstall hidden for the default version — agent
                      blocks anyway, but better UX to hide than 409. */}
                  {!isDefault && (
                    <Popconfirm
                      title={`Uninstall PHP ${record.version}?`}
                      description="apt-get purge --auto-remove. Sites pinned to this version will break until you change their pool."
                      okText="Uninstall"
                      okButtonProps={{ danger: true }}
                      cancelText="Cancel"
                      onConfirm={() => handleUninstall(record.version)}
                    >
                      <RowActionButton
                        icon={<DeleteOutlined />}
                        danger
                        loading={isUninstalling}
                        disabled={isUninstalling || isReloading}
                      >
                        Uninstall
                      </RowActionButton>
                    </Popconfirm>
                  )}
                </Space>
              );
            } else {
              return (
                <RowActionButton
                  icon={<DownloadOutlined />}
                  onClick={() => handleInstall(record.version)}
                  loading={isInstalling}
                  disabled={isInstalling}
                >
                  Install
                </RowActionButton>
              );
            }
          }}
        />
      </Table>
    </>
  );
};
