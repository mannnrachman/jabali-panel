import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Input,
  notification,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from "antd";
import { ApiOutlined, DownloadOutlined, SearchOutlined } from "@icons";
import { apiClient } from "../../../apiClient";

// Shape mirrors the contract locked in panel-api/internal/agent/php_ext_contract_test.go.
interface ExtensionState {
  name: string;
  installed: boolean;
  enabled: boolean;
  built_in: boolean;
}
interface ExtListResponse {
  version: string;
  extensions: ExtensionState[];
}
interface VersionStatus {
  version: string;
  installed: boolean;
  fpm_running: boolean;
}
interface VersionStatusResponse {
  default_version: string;
  versions: VersionStatus[];
}

type ApplyAction = "install" | "remove" | "enable" | "disable";

export const PHPExtensionsTab = () => {
  const [versions, setVersions] = useState<string[]>([]);
  const [selectedVersion, setSelectedVersion] = useState<string | null>(null);
  const [extensions, setExtensions] = useState<ExtensionState[]>([]);
  const [loadingVersions, setLoadingVersions] = useState(true);
  const [loadingExtensions, setLoadingExtensions] = useState(false);
  const [busyExt, setBusyExt] = useState<string | null>(null);
  const [search, setSearch] = useState("");

  // Fetch installed versions once. Only installed versions populate the dropdown.
  useEffect(() => {
    const run = async () => {
      try {
        const { data } = await apiClient.get<VersionStatusResponse>(
          "/admin/php/versions/status"
        );
        const installed = data.versions.filter((v) => v.installed).map((v) => v.version);
        installed.sort((a, b) => {
          const pa = a.split(".").map(Number);
          const pb = b.split(".").map(Number);
          for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
            const diff = (pb[i] ?? 0) - (pa[i] ?? 0);
            if (diff !== 0) return diff;
          }
          return 0;
        });
        setVersions(installed);
        const def = installed.includes(data.default_version)
          ? data.default_version
          : installed[0] ?? null;
        setSelectedVersion(def);
      } catch (err) {
        notification.error({
          message: "Failed to fetch PHP versions",
          description: err instanceof Error ? err.message : "Unknown error",
        });
      } finally {
        setLoadingVersions(false);
      }
    };
    run();
  }, []);

  // Refetch extensions whenever selected version changes.
  useEffect(() => {
    if (!selectedVersion) return;
    loadExtensions(selectedVersion);
    // loadExtensions closes over setState only, safe to omit from deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedVersion]);

  const loadExtensions = async (version: string) => {
    setLoadingExtensions(true);
    try {
      const { data } = await apiClient.get<ExtListResponse>(
        `/admin/php/versions/${version}/extensions`
      );
      setExtensions(data.extensions);
    } catch (err) {
      notification.error({
        message: `Failed to fetch extensions for PHP ${version}`,
        description: err instanceof Error ? err.message : "Unknown error",
      });
      setExtensions([]);
    } finally {
      setLoadingExtensions(false);
    }
  };

  const handleApply = async (ext: string, action: ApplyAction) => {
    if (!selectedVersion) return;
    setBusyExt(ext);
    try {
      await apiClient.post(
        `/admin/php/versions/${selectedVersion}/extensions/${ext}/apply`,
        { action }
      );
      notification.success({
        message: `${ext}: ${action} applied for PHP ${selectedVersion}`,
        duration: 2,
      });
      // Server is source of truth; refetch instead of optimistic update.
      await loadExtensions(selectedVersion);
    } catch (err) {
      notification.error({
        message: `${action} ${ext} failed`,
        description: err instanceof Error ? err.message : "Unknown error",
        duration: 5,
      });
    } finally {
      setBusyExt(null);
    }
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return extensions;
    return extensions.filter((e) => e.name.toLowerCase().includes(q));
  }, [extensions, search]);

  if (loadingVersions) {
    return (
      <div style={{ textAlign: "center" }}>
        <Spin size="large" />
      </div>
    );
  }

  if (versions.length === 0) {
    return (
      <Alert
        type="info"
        showIcon
        title="No PHP versions installed"
        description="Install a PHP version first under the PHP Versions tab. Extension management requires at least one installed version."
      />
    );
  }

  return (
    <>
      <Space style={{ marginBottom: 16 }} align="center">
        <Typography.Text strong>PHP Version</Typography.Text>
        <Select
          style={{ width: 160 }}
          value={selectedVersion ?? undefined}
          onChange={(v) => setSelectedVersion(v)}
          options={versions.map((v) => ({ value: v, label: `PHP ${v}` }))}
          aria-label="PHP version"
        />
      </Space>

      <Card
        title={selectedVersion ? `PHP ${selectedVersion} Extensions` : "Extensions"}
      >
        <Table<ExtensionState>
          dataSource={filtered}
          rowKey="name"
          loading={loadingExtensions}
          pagination={false}
          size="middle"
          scroll={{ x: "max-content" }}
        >
          <Table.Column<ExtensionState>
            dataIndex="name"
            title="Extension"
            sorter={(a, b) => a.name.localeCompare(b.name)}
            filterIcon={() => <SearchOutlined />}
            // Column-level search wired to the same `search` state that
            // drives the previous top-right Input.Search; the icon in
            // the column header reveals a popover so operators see the
            // filter affordance without scanning for the external
            // search box.
            filterDropdown={({ confirm, close }) => (
              <div style={{ padding: 8, minWidth: 220 }}>
                <Input.Search
                  placeholder="Search extensions"
                  allowClear
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onSearch={() => {
                    confirm({ closeDropdown: false });
                    close();
                  }}
                />
              </div>
            )}
            render={(name: string, record) => (
              <Space>
                <ApiOutlined />
                <span style={{ fontFamily: "monospace" }}>{name}</span>
                {record.built_in && <Tag>built-in</Tag>}
              </Space>
            )}
          />
          <Table.Column<ExtensionState>
            dataIndex="enabled"
            title="Status"
            width={120}
            sorter={(a, b) => Number(b.enabled) - Number(a.enabled)}
            render={(enabled: boolean) =>
              enabled ? <Tag color="green">Enabled</Tag> : <Tag>Disabled</Tag>
            }
          />
          <Table.Column<ExtensionState>
            dataIndex="installed"
            title="Installed"
            width={120}
            sorter={(a, b) => Number(b.installed) - Number(a.installed)}
            render={(installed: boolean) =>
              installed ? <Tag color="green">Yes</Tag> : <Tag>No</Tag>
            }
          />
          <Table.Column<ExtensionState>
            title="Action"
            width={260}
            render={(_: unknown, record) => (
              <ExtensionActions
                record={record}
                busy={busyExt === record.name}
                onApply={(a) => handleApply(record.name, a)}
              />
            )}
          />
        </Table>
      </Card>
    </>
  );
};

interface ExtensionActionsProps {
  record: ExtensionState;
  busy: boolean;
  onApply: (action: ApplyAction) => void;
}

// ExtensionActions decides which buttons to show based on the record state.
// mysql is special: it's a meta-install (installs mysqli + pdo_mysql as a group)
// but enable/disable are rejected by the agent as ambiguous. So the row offers
// only Install / Remove for mysql, never Enable / Disable.
const ExtensionActions = ({ record, busy, onApply }: ExtensionActionsProps) => {
  const isMysqlMeta = record.name === "mysql";

  // Built-ins: apt install/remove rejected; only enable/disable.
  if (record.built_in) {
    return record.enabled ? (
      <Button type="text" danger loading={busy} onClick={() => onApply("disable")}>
        Disable
      </Button>
    ) : (
      <Button type="text" loading={busy} onClick={() => onApply("enable")}>
        Enable
      </Button>
    );
  }

  // Not installed: only Install.
  if (!record.installed) {
    return (
      <Button
        type="text"
        icon={<DownloadOutlined />}
        loading={busy}
        onClick={() => onApply("install")}
      >
        Install
      </Button>
    );
  }

  // Installed: show Remove plus Enable/Disable (except mysql meta).
  return (
    <Space>
      {!isMysqlMeta &&
        (record.enabled ? (
          <Button type="text" danger loading={busy} onClick={() => onApply("disable")}>
            Disable
          </Button>
        ) : (
          <Button type="text" loading={busy} onClick={() => onApply("enable")}>
            Enable
          </Button>
        ))}
      <Button type="text" danger loading={busy} onClick={() => onApply("remove")}>
        Remove
      </Button>
    </Space>
  );
};
