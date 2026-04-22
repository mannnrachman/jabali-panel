import { useState, useEffect } from "react";
import {
  ArrowLeftOutlined,
  DeleteOutlined,
  EditOutlined,
  LockOutlined,
  CheckOutlined,
  CloseOutlined,
} from "@ant-design/icons";
import {
  Button,
  Space,
  Table,
  Tag,
  Typography,
  Card,
  Input,
  Select,
  InputNumber,
  Collapse,
  Switch,
  Row,
  Col,
  Popconfirm,
  Spin,
  Empty,
  notification,
  theme,
} from "antd";
import { useLocation, useNavigate, useParams } from "react-router";

// Post-M21 notify shim — matches the Refine useNotification().open
// contract so the call sites below need no other change.
//
// The `open` function is defined at module scope (not inside the hook)
// so it has a STABLE reference across renders. Returning a freshly-
// allocated { open: ... } object on every call would make `open`'s
// identity churn, and useEffects that include `open` in their deps
// would re-fire every render → infinite refetch loop → API 429s.
// That's exactly the bug the DNS Records page hit (rate-limited
// "Failed to load" notification stack).
type NotifyInput = {
  type?: "success" | "error" | "warning" | "info";
  message: string;
  description?: React.ReactNode;
};
const stableNotifyOpen = (input: NotifyInput) => {
  notification.open({
    message: input.message,
    description: input.description,
    type: input.type,
  });
};
const stableNotify = { open: stableNotifyOpen };
function useNotification() {
  return stableNotify;
}

import { apiClient } from "../../apiClient";

// Type definitions
type DNSRecordType = "A" | "AAAA" | "CNAME" | "MX" | "TXT" | "NS";

// recordTypeColor maps each DNS record type to an AntD Tag colour so
// the table scans visually — A/AAAA share the blue family (IPv4/IPv6
// address records), MX is orange (mail), TXT is green (metadata/SPF/
// DMARC), CNAME is purple (aliases), NS is magenta (delegation).
const recordTypeColor: Record<DNSRecordType, string> = {
  A: "blue",
  AAAA: "geekblue",
  CNAME: "purple",
  MX: "orange",
  TXT: "green",
  NS: "magenta",
};

interface DNSZone {
  id: string;
  domain_id: string;
  is_enabled: boolean;
  serial: number;
  refresh_seconds: number;
  retry_seconds: number;
  expire_seconds: number;
  minimum_ttl: number;
  created_at: string;
  updated_at: string;
}

interface DNSRecord {
  id: string;
  zone_id: string;
  name: string;
  type: DNSRecordType;
  content: string;
  ttl: number;
  priority?: number;
  managed: boolean;
  is_enabled: boolean;
  created_at: string;
  updated_at: string;
}

interface Domain {
  id: string;
  name: string;
}

// Type-aware placeholder helpers
const getPlaceholders = (
  type: DNSRecordType
): { nameHelper: string; contentHelper: string } => {
  switch (type) {
    case "A":
      return {
        nameHelper: "e.g. www, @ (for root), blog",
        contentHelper: "IPv4 address, e.g. 192.0.2.1",
      };
    case "AAAA":
      return {
        nameHelper: "e.g. www, @ (for root), blog",
        contentHelper: "IPv6 address, e.g. 2001:db8::1",
      };
    case "CNAME":
      return {
        nameHelper: "e.g. blog (subdomain name)",
        contentHelper: "Target domain, e.g. example.com or target.example.com.",
      };
    case "MX":
      return {
        nameHelper: "Usually @ (root)",
        contentHelper: "Mail server, e.g. mail.example.com",
      };
    case "TXT":
      return {
        nameHelper: "e.g. @, _dmarc, _acme-challenge",
        contentHelper: 'Text value in quotes, e.g. "v=spf1 mx ~all"',
      };
    case "NS":
      return {
        nameHelper: "e.g. sub (subdomain)",
        contentHelper: "Nameserver, e.g. ns.external.com",
      };
  }
};

interface EditingRecord extends DNSRecord {
  originalId: string;
}

export const DNSRecordsPage = () => {
  const { id: domainId } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const { open } = useNotification();
  const { token } = theme.useToken();

  // Back-link target: DNS list in the same shell we're currently in.
  // Admin path prefix is /jabali-admin; user path prefix is /jabali-panel.
  const dnsListPath = location.pathname.startsWith("/jabali-admin")
    ? "/jabali-admin/dns"
    : "/jabali-panel/dns";

  // State
  const [domain, setDomain] = useState<Domain | null>(null);
  const [zone, setZone] = useState<DNSZone | null>(null);
  const [records, setRecords] = useState<DNSRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [zoneNotProvisioned, setZoneNotProvisioned] = useState(false);
  const [savingZone, setSavingZone] = useState(false);
  const [deletingRecordId, setDeletingRecordId] = useState<string | null>(null);

  // Add record form state
  const [addRecordType, setAddRecordType] = useState<DNSRecordType>("A");
  const [addRecordName, setAddRecordName] = useState("");
  const [addRecordContent, setAddRecordContent] = useState("");
  const [addRecordPriority, setAddRecordPriority] = useState<number | null>(null);
  const [addRecordTTL, setAddRecordTTL] = useState(3600);
  const [addingRecord, setAddingRecord] = useState(false);

  // Edit mode state
  const [editingRecord, setEditingRecord] = useState<EditingRecord | null>(null);
  const [editRecordName, setEditRecordName] = useState("");
  const [editRecordContent, setEditRecordContent] = useState("");
  const [editRecordPriority, setEditRecordPriority] = useState<number | null>(
    null
  );
  const [editRecordTTL, setEditRecordTTL] = useState(3600);
  const [savingEdit, setSavingEdit] = useState(false);

  // Load domain data
  useEffect(() => {
    const loadDomain = async () => {
      try {
        const res = await apiClient.get(`/domains/${domainId}`);
        setDomain(res.data);
      } catch (err) {
        open?.({
          type: "error",
          message: "Failed to load domain",
        });
      }
    };

    if (domainId) {
      loadDomain();
    }
  }, [domainId, open]);

  // Load zone and records
  useEffect(() => {
    const loadDnsData = async () => {
      if (!domainId) return;

      setLoading(true);
      try {
        // Load zone data
        try {
          const zoneRes = await apiClient.get(`/domains/${domainId}/dns/zone`);
          setZone(zoneRes.data.zone);
          setZoneNotProvisioned(false);
        } catch (err: unknown) {
          const e = err as {
            response?: { status?: number; data?: { error?: string } };
          };
          if (e.response?.status === 404 && e.response?.data?.error === "zone_not_provisioned") {
            setZoneNotProvisioned(true);
            setZone(null);
          } else {
            throw err;
          }
        }

        // Load records
        const recordsRes = await apiClient.get(
          `/domains/${domainId}/dns/records`
        );
        setRecords(recordsRes.data.records);
      } catch (err) {
        const e = err as {
          response?: { data?: { detail?: string } };
          message?: string;
        };
        open?.({
          type: "error",
          message: "Failed to load DNS data",
          description:
            e.response?.data?.detail ?? e.message ?? "Unknown error",
        });
      } finally {
        setLoading(false);
      }
    };

    loadDnsData();
  }, [domainId, open]);

  // Handle zone settings save
  const handleZoneSave = async () => {
    if (!zone || !domainId) return;

    setSavingZone(true);
    try {
      const res = await apiClient.patch(`/domains/${domainId}/dns/zone`, {
        refresh_seconds: zone.refresh_seconds,
        retry_seconds: zone.retry_seconds,
        expire_seconds: zone.expire_seconds,
        minimum_ttl: zone.minimum_ttl,
        is_enabled: zone.is_enabled,
      });

      setZone(res.data.zone);
      open?.({
        type: "success",
        message: "Zone settings saved",
      });
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      open?.({
        type: "error",
        message: "Failed to save zone settings",
        description:
          e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setSavingZone(false);
    }
  };

  // Handle add record
  const handleAddRecord = async () => {
    if (!domainId || !addRecordName || !addRecordContent) {
      open?.({
        type: "error",
        message: "Name and content are required",
      });
      return;
    }

    setAddingRecord(true);
    try {
      const res = await apiClient.post(
        `/domains/${domainId}/dns/records`,
        {
          name: addRecordName,
          type: addRecordType,
          content: addRecordContent,
          ttl: addRecordTTL,
          ...(addRecordType === "MX" && addRecordPriority !== null
            ? { priority: addRecordPriority }
            : {}),
        }
      );

      setRecords([...records, res.data.record]);
      setAddRecordName("");
      setAddRecordContent("");
      setAddRecordPriority(null);
      setAddRecordTTL(3600);
      setAddRecordType("A");

      open?.({
        type: "success",
        message: "Record added",
      });
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      open?.({
        type: "error",
        message: "Failed to add record",
        description:
          e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setAddingRecord(false);
    }
  };

  // Handle edit mode entry
  const handleEditStart = (record: DNSRecord) => {
    setEditingRecord({
      ...record,
      originalId: record.id,
    });
    setEditRecordName(record.name);
    setEditRecordContent(record.content);
    setEditRecordPriority(record.priority ?? null);
    setEditRecordTTL(record.ttl);
  };

  // Handle edit save
  const handleEditSave = async () => {
    if (!editingRecord || !editRecordName || !editRecordContent) {
      open?.({
        type: "error",
        message: "Name and content are required",
      });
      return;
    }

    setSavingEdit(true);
    try {
      const res = await apiClient.patch(
        `/dns/records/${editingRecord.originalId}`,
        {
          name: editRecordName,
          content: editRecordContent,
          ttl: editRecordTTL,
          ...(editingRecord.type === "MX" && editRecordPriority !== null
            ? { priority: editRecordPriority }
            : {}),
        }
      );

      setRecords(
        records.map((r) =>
          r.id === editingRecord.originalId ? res.data.record : r
        )
      );
      setEditingRecord(null);

      open?.({
        type: "success",
        message: "Record updated",
      });
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      open?.({
        type: "error",
        message: "Failed to update record",
        description:
          e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setSavingEdit(false);
    }
  };

  // Handle edit cancel
  const handleEditCancel = () => {
    setEditingRecord(null);
  };

  // Handle record delete
  const handleDeleteRecord = async (recordId: string) => {
    setDeletingRecordId(recordId);
    try {
      await apiClient.delete(`/dns/records/${recordId}`);
      setRecords(records.filter((r) => r.id !== recordId));
      open?.({
        type: "success",
        message: "Record deleted",
      });
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      open?.({
        type: "error",
        message: "Failed to delete record",
        description:
          e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setDeletingRecordId(null);
    }
  };

  const placeholders = getPlaceholders(addRecordType);
  const editPlaceholders = editingRecord
    ? getPlaceholders(editingRecord.type)
    : placeholders;

  // Check if a record should be read-only
  const isRecordReadOnly = (record: DNSRecord) => {
    const recordType = record.type as string;
    return record.managed && (recordType === "SOA" || recordType === "NS");
  };

  // Filter out SOA records from display (type guard)
  const displayRecords = records.filter((r) => {
    const recordType = r.type as string;
    return recordType !== "SOA";
  });

  if (loading) {
    return (
      <div style={{ textAlign: "center" }}>
        <Spin />
      </div>
    );
  }

  if (zoneNotProvisioned) {
    return (
      <div >
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          onClick={() => navigate(dnsListPath)}
          style={{ marginBottom: 16 }}
        >
          Back to DNS
        </Button>

        <Card
          style={{
            maxWidth: 600,
            margin: "0 auto",
            textAlign: "center",
            marginTop: 60,
          }}
        >
          <Typography.Title level={4}>
            DNS Zone Not Provisioned
          </Typography.Title>
          <Typography.Paragraph>
            The DNS zone for this domain has not yet been provisioned. This
            normally happens automatically on domain creation. If you just
            created this domain, give the reconciler ~60 seconds. Otherwise, try
            saving the domain from the domain list to re-trigger provisioning.
          </Typography.Paragraph>
        </Card>
      </div>
    );
  }

  return (
    <div >
      {/* Header */}
      <Button
        type="text"
        icon={<ArrowLeftOutlined />}
        onClick={() => navigate(dnsListPath)}
        style={{ marginBottom: 16 }}
      >
        Back to DNS
      </Button>

      <Space style={{ marginBottom: 16, width: "100%" }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          DNS Records for {domain?.name}
        </Typography.Title>
      </Space>

      {zone && (
        <Typography.Text
          type="secondary"
          style={{ display: "block", marginBottom: 24 }}
        >
          Zone serial {zone.serial} • {displayRecords.length} records
        </Typography.Text>
      )}

      {/* Zone Settings */}
      {zone && (
        <Collapse
          style={{ marginBottom: 24 }}
          items={[
            {
              key: "zone-settings",
              label: "Zone Settings",
              children: (
                <div>
                  <Row gutter={16} style={{ marginBottom: 16 }}>
                    <Col span={12}>
                      <div style={{ marginBottom: 8 }}>
                        <Typography.Text>Refresh (seconds)</Typography.Text>
                      </div>
                      <InputNumber
                        min={0}
                        value={zone.refresh_seconds}
                        onChange={(v) =>
                          setZone({ ...zone, refresh_seconds: v ?? 0 })
                        }
                        style={{ width: "100%" }}
                      />
                    </Col>
                    <Col span={12}>
                      <div style={{ marginBottom: 8 }}>
                        <Typography.Text>Retry (seconds)</Typography.Text>
                      </div>
                      <InputNumber
                        min={0}
                        value={zone.retry_seconds}
                        onChange={(v) =>
                          setZone({ ...zone, retry_seconds: v ?? 0 })
                        }
                        style={{ width: "100%" }}
                      />
                    </Col>
                  </Row>

                  <Row gutter={16} style={{ marginBottom: 16 }}>
                    <Col span={12}>
                      <div style={{ marginBottom: 8 }}>
                        <Typography.Text>Expire (seconds)</Typography.Text>
                      </div>
                      <InputNumber
                        min={0}
                        value={zone.expire_seconds}
                        onChange={(v) =>
                          setZone({ ...zone, expire_seconds: v ?? 0 })
                        }
                        style={{ width: "100%" }}
                      />
                    </Col>
                    <Col span={12}>
                      <div style={{ marginBottom: 8 }}>
                        <Typography.Text>Minimum TTL (seconds)</Typography.Text>
                      </div>
                      <InputNumber
                        min={0}
                        value={zone.minimum_ttl}
                        onChange={(v) =>
                          setZone({ ...zone, minimum_ttl: v ?? 0 })
                        }
                        style={{ width: "100%" }}
                      />
                    </Col>
                  </Row>

                  <Row gutter={16} style={{ marginBottom: 16 }}>
                    <Col span={24}>
                      <Space>
                        <Typography.Text>Enabled</Typography.Text>
                        <Switch checkedChildren={<CheckOutlined />} unCheckedChildren={<CloseOutlined />}
                          checked={zone.is_enabled}
                          onChange={(v) =>
                            setZone({ ...zone, is_enabled: v })
                          }
                        />
                      </Space>
                    </Col>
                  </Row>

                  <Button
                    type="primary"
                    onClick={handleZoneSave}
                    loading={savingZone}
                  >
                    Save Zone Settings
                  </Button>
                </div>
              ),
            },
          ]}
        />
      )}

      {/* Records Table */}
      {displayRecords.length === 0 ? (
        <Card style={{ marginBottom: 24 }}>
          <Empty description="No DNS records yet" />
        </Card>
      ) : (
        <Card style={{ marginBottom: 24 }}>
          <div>
            {editingRecord ? (
              <div
                style={{
                  padding: 16,
                  border: `1px solid ${token.colorBorderSecondary}`,
                  borderRadius: token.borderRadius,
                  marginBottom: 16,
                }}
              >
                <Typography.Text strong>Edit Record</Typography.Text>
                <Row gutter={16} style={{ marginTop: 12, marginBottom: 12 }}>
                  <Col span={6}>
                    <div style={{ marginBottom: 8 }}>
                      <Typography.Text>Name</Typography.Text>
                    </div>
                    <Input
                      value={editRecordName}
                      onChange={(e) => setEditRecordName(e.target.value)}
                      placeholder={editPlaceholders.nameHelper}
                    />
                  </Col>
                  <Col span={4}>
                    <div style={{ marginBottom: 8 }}>
                      <Typography.Text>Type</Typography.Text>
                    </div>
                    <Input value={editingRecord.type} disabled />
                  </Col>
                  <Col span={10}>
                    <div style={{ marginBottom: 8 }}>
                      <Typography.Text>Content</Typography.Text>
                    </div>
                    <Input
                      value={editRecordContent}
                      onChange={(e) => setEditRecordContent(e.target.value)}
                      placeholder={editPlaceholders.contentHelper}
                    />
                  </Col>
                  <Col span={4}>
                    <div style={{ marginBottom: 8 }}>
                      <Typography.Text>TTL</Typography.Text>
                    </div>
                    <InputNumber
                      min={0}
                      value={editRecordTTL}
                      onChange={(v) => setEditRecordTTL(v ?? 3600)}
                      style={{ width: "100%" }}
                    />
                  </Col>
                </Row>
                {editingRecord.type === "MX" && (
                  <Row gutter={16} style={{ marginBottom: 12 }}>
                    <Col span={6}>
                      <div style={{ marginBottom: 8 }}>
                        <Typography.Text>
                          Priority
                        </Typography.Text>
                      </div>
                      <InputNumber
                        min={0}
                        value={editRecordPriority ?? 0}
                        onChange={(v) => setEditRecordPriority(v ?? null)}
                        style={{ width: "100%" }}
                      />
                    </Col>
                  </Row>
                )}
                <Space>
                  <Button
                    type="primary"
                    onClick={handleEditSave}
                    loading={savingEdit}
                  >
                    Save
                  </Button>
                  <Button onClick={handleEditCancel}>
                    Cancel
                  </Button>
                </Space>
              </div>
            ) : null}

            <Table<DNSRecord>
              dataSource={displayRecords}
              rowKey="id"
              pagination={false}
              scroll={{ x: "max-content" }}
              columns={[
                {
                  title: "Name",
                  dataIndex: "name",
                  key: "name",
                  width: "15%",
                  render: (text: string) => text || "@",
                },
                {
                  title: "Type",
                  dataIndex: "type",
                  key: "type",
                  width: "10%",
                  render: (_: unknown, record: DNSRecord) => (
                    <Tag color={recordTypeColor[record.type] ?? "default"}>
                      {record.type}
                    </Tag>
                  ),
                },
                {
                  title: "Content",
                  dataIndex: "content",
                  key: "content",
                  width: "30%",
                  render: (text: string) => (
                    <Typography.Text
                      style={{ fontFamily: "monospace" }}
                    >
                      {text}
                    </Typography.Text>
                  ),
                },
                {
                  title: "TTL",
                  dataIndex: "ttl",
                  key: "ttl",
                  width: "10%",
                },
                {
                  title: "Priority",
                  dataIndex: "priority",
                  key: "priority",
                  width: "10%",
                  render: (priority: number | undefined) =>
                    priority !== undefined ? priority : "—",
                },
                {
                  title: "Actions",
                  key: "actions",
                  width: "25%",
                  render: (_: unknown, record: DNSRecord) => {
                    const readonly = isRecordReadOnly(record);

                    if (readonly) {
                      return (
                        <Space>
                          <LockOutlined />
                          <Typography.Text
                            type="secondary"
                          >
                            Managed
                          </Typography.Text>
                        </Space>
                      );
                    }

                    return (
                      <Space>
                        <Button
                          type="text"
                          icon={<EditOutlined />}
                          onClick={() => handleEditStart(record)}
                        />
                        <Popconfirm
                          title="Delete record?"
                          description="This action cannot be undone."
                          onConfirm={() => handleDeleteRecord(record.id)}
                          okText="Delete"
                          okButtonProps={{ danger: true }}
                        >
                          <Button
                            type="text"
                            danger
                            icon={<DeleteOutlined />}
                            loading={deletingRecordId === record.id}
                          />
                        </Popconfirm>
                      </Space>
                    );
                  },
                },
              ]}
            />
          </div>
        </Card>
      )}

      {/* Add Record Card */}
      <Card title="Add Record">
        <Row gutter={16} style={{ marginBottom: 16 }}>
          <Col span={12}>
            <div style={{ marginBottom: 8 }}>
              <Typography.Text>Type</Typography.Text>
            </div>
            <Select
              value={addRecordType}
              onChange={setAddRecordType}
              options={[
                { value: "A", label: "A" },
                { value: "AAAA", label: "AAAA" },
                { value: "CNAME", label: "CNAME" },
                { value: "MX", label: "MX" },
                { value: "TXT", label: "TXT" },
                { value: "NS", label: "NS" },
              ]}
              style={{ width: "100%" }}
            />
          </Col>
          <Col span={12}>
            <div style={{ marginBottom: 8 }}>
              <Typography.Text>Name</Typography.Text>
            </div>
            <Input
              placeholder={placeholders.nameHelper}
              value={addRecordName}
              onChange={(e) => setAddRecordName(e.target.value)}
            />
          </Col>
        </Row>

        <Row gutter={16} style={{ marginBottom: 16 }}>
          <Col span={24}>
            <div style={{ marginBottom: 8 }}>
              <Typography.Text>Content</Typography.Text>
            </div>
            <Input
              placeholder={placeholders.contentHelper}
              value={addRecordContent}
              onChange={(e) => setAddRecordContent(e.target.value)}
            />
          </Col>
        </Row>

        <Row gutter={16} style={{ marginBottom: 16 }}>
          <Col span={12}>
            <div style={{ marginBottom: 8 }}>
              <Typography.Text>TTL (seconds)</Typography.Text>
            </div>
            <InputNumber
              min={0}
              value={addRecordTTL}
              onChange={(v) => setAddRecordTTL(v ?? 3600)}
              style={{ width: "100%" }}
            />
          </Col>
          {addRecordType === "MX" && (
            <Col span={12}>
              <div style={{ marginBottom: 8 }}>
                <Typography.Text>Priority</Typography.Text>
              </div>
              <InputNumber
                min={0}
                value={addRecordPriority}
                onChange={setAddRecordPriority}
                placeholder="e.g. 10"
                style={{ width: "100%" }}
              />
            </Col>
          )}
        </Row>

        <Button
          type="primary"
          onClick={handleAddRecord}
          loading={addingRecord}
        >
          Add Record
        </Button>
      </Card>
    </div>
  );
};
