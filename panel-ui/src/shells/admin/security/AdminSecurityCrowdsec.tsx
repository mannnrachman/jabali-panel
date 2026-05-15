// AdminSecurityCrowdsec — M26 Step 7. Four cards: metrics, active
// decisions (with add Drawer + delete Popconfirm), bouncers (read-only),
// hub items (read-only). Polls metrics + status every 30s.
//
// Conventions: per docs/CONVENTIONS.md the "create" affordance is a
// Drawer (not a Modal), Tables consume <Table.Column> children (not a
// columns prop), and Statistic rows lay out via Row gutter rather than
// inline marginLeft. Hooks stay direct useQuery (not useTableURL) —
// these endpoints are not the standard {data,total,page,page_size}
// list shape; they're agent passthroughs.
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Empty,
  Form,
  Grid,
  Input,
  message,
  Popconfirm,
  Radio,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from "antd";
import type { ReactNode } from "react";
import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import {
  ApiOutlined,
  AppstoreOutlined,
  BellOutlined,
  CheckCircleOutlined,
  FileTextOutlined,
  SafetyOutlined,
  ThunderboltOutlined,
  WarningOutlined,
  DeleteOutlined,
  QuestionCircleOutlined,
  ReloadOutlined,
} from "@icons";
import { RowActionButton } from "../../../components/RowActionButton";
import { ISO3166_COUNTRIES } from "../../../data/iso3166";
import { CrowdsecTestIPCard } from "./CrowdsecTestIPCard";

import {
  useAddCrowdsecAllowlist,
  useAddCrowdsecDecision,
  useAppSecGeoblock,
  useCrowdsecAlert,
  useCrowdsecAlerts,
  useCrowdsecAllowlists,
  useCrowdsecBlocklists,
  useRefreshCrowdsecBlocklists,
  useCrowdsecBouncers,
  useCrowdsecDecisions,
  useCrowdsecHub,
  useInstallCrowdsecHubItem,
  useRemoveCrowdsecHubItem,
  useCrowdsecMetrics,
  useCrowdsecCaptcha,
  useCrowdsecConsoleEnrollment,
  useCrowdsecConsoleStatus,
  useDisenrollCrowdsecConsole,
  useCrowdsecProfiles,
  useCrowdsecStatus,
  useDeleteCrowdsecDecision,
  useEnrollCrowdsecConsole,
  useRemoveCrowdsecAllowlist,
  useToggleCrowdsecConsoleOption,
  useUpdateAppSecGeoblock,
  useUpdateCrowdsecCaptcha,
  useUpdateCrowdsecProfiles,
  type AppSecGeoblockMode,
  type CrowdsecAlert,
  type CrowdsecAllowlistEntry,
  type CrowdsecCaptchaProvider,
  type CrowdsecConsoleOption,
  type CrowdsecDecision,
  type CrowdsecProfileOverride,
  type CrowdsecScenarioItem,
  type CrowdsecScope,
} from "../../../hooks/useSecurityCrowdsec";

const SCOPE_OPTIONS: Array<{ value: CrowdsecScope | "all"; label: string }> = [
  { value: "all", label: "All scopes" },
  { value: "ip", label: "IP" },
  { value: "range", label: "Range (CIDR)" },
  { value: "country", label: "Country" },
  { value: "as", label: "AS" },
];

// Per-scope value-field validation. Server is authoritative (agent
// does net.ParseIP / net.ParseCIDR / 2-letter country / ASN digits) —
// these client-side patterns exist only to reject typos before a
// round-trip. All four scopes ship with their own placeholder + help.
const IP_OR_CIDR = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/;
const COUNTRY_CODE = /^[A-Za-z]{2}$/;
const ASN_RE = /^(AS|as)?\d+$/;
// CrowdSec accepts Go time.ParseDuration: 4h, 1h30m, 30m, 1d (custom).
const DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h|d))+$/;

const ADD_SCOPE_OPTIONS: Array<{ value: CrowdsecScope; label: string }> = [
  { value: "ip", label: "IP address" },
  { value: "range", label: "Range (CIDR)" },
  { value: "country", label: "Country (ISO 3166-1)" },
  { value: "as", label: "AS (ASN)" },
];

type AddDecisionFormValues = {
  scope: CrowdsecScope;
  value: string;
  duration: string;
  reason: string;
};

const fmtTime = (s?: string): string => (s ? new Date(s).toLocaleString() : "—");

// MetricTile renders one CrowdSec metric as an icon-led panel inside a
// responsive grid Col. Tint is the icon background only — text stays
// theme-default so light/dark themes both render correctly. `value`
// accepts a number (formatted via toLocaleString at 22px) OR a ReactNode
// (rendered raw at 16px so chips/tags fit the same tile shape).
function MetricTile({
  icon,
  tint,
  label,
  value,
  hint,
}: {
  icon: ReactNode;
  tint: string;
  label: string;
  value: number | ReactNode;
  hint: string;
}) {
  const isNumber = typeof value === "number";
  return (
    <Col flex="1 1 200px" style={{ minWidth: 180 }}>
      <Tooltip title={hint}>
        <Card
          size="small"
          hoverable
          styles={{ body: { padding: 12 } }}
          style={{ height: "100%" }}
        >
          <Space size={12} align="center" style={{ width: "100%" }}>
            <div
              style={{
                width: 44,
                height: 44,
                borderRadius: 8,
                background: `${tint}22`,
                color: tint,
                fontSize: 22,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                flexShrink: 0,
              }}
            >
              {icon}
            </div>
            <div style={{ minWidth: 0, flex: 1 }}>
              <Typography.Text type="secondary" style={{ fontSize: 12, display: "block" }}>
                {label}
              </Typography.Text>
              {isNumber ? (
                <Typography.Text strong style={{ fontSize: 22, lineHeight: 1.1 }}>
                  {(value as number).toLocaleString()}
                </Typography.Text>
              ) : (
                <div style={{ fontSize: 14, lineHeight: 1.2 }}>{value}</div>
              )}
            </div>
          </Space>
        </Card>
      </Tooltip>
    </Col>
  );
}

export const AdminSecurityCrowdsec = () => {
  const status = useCrowdsecStatus();
  const metrics = useCrowdsecMetrics();
  const [scope, setScope] = useState<CrowdsecScope | "all">("all");
  const decisions = useCrowdsecDecisions(scope === "all" ? undefined : scope);
  const bouncers = useCrowdsecBouncers();
  const hub = useCrowdsecHub();
  const addDecision = useAddCrowdsecDecision();
  const deleteDecision = useDeleteCrowdsecDecision();

  const [addOpen, setAddOpen] = useState(false);
  const [addForm] = Form.useForm<AddDecisionFormValues>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg ?? (typeof window !== "undefined" ? window.innerWidth >= 992 : true);

  const submitAdd = async (values: AddDecisionFormValues) => {
    try {
      await addDecision.mutateAsync(values);
      message.success(`Decision added: ${values.scope}=${values.value}`);
      setAddOpen(false);
      addForm.resetFields();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to add decision");
    }
  };

  const onDeleteDecision = async (row: CrowdsecDecision) => {
    try {
      await deleteDecision.mutateAsync(row.id);
      message.success(`Removed ban on ${row.ip}`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to remove decision");
    }
  };

  // Sub-tabs (one card per tab). URL-driven via ?sub= so a direct link
  // deep-links to a specific sub-tab. Keep the Add-decision Drawer
  // OUTSIDE the Tabs so it stays open across tab switches (rare, but
  // the Drawer should not unmount mid-form-fill).
  const [sp, setSp] = useSearchParams();
  const subTabs = [
    "overview",
    "decisions",
    "allowlist",
    "alerts",
    "console",
    "captcha",
    "profiles",
    "appsec",
    "blocklists",
    "bouncers",
    "hub",
  ] as const;
  type SubTab = (typeof subTabs)[number];
  const activeSub: SubTab = ((): SubTab => {
    const s = sp.get("sub");
    return (subTabs as readonly string[]).includes(s ?? "") ? (s as SubTab) : "overview";
  })();
  const onSubChange = (key: string) => {
    setSp((prev) => {
      const next = new URLSearchParams(prev);
      next.set("sub", key);
      return next;
    });
  };

  const overviewPanel = (
    <Space direction="vertical" size="large" style={{ width: "100%" }}>
      {metrics.isLoading ? (
        <Typography.Text type="secondary">Loading…</Typography.Text>
      ) : (
        <Row gutter={[12, 12]}>
          <MetricTile
            icon={<CheckCircleOutlined />}
            tint={status.data?.running ? "#52c41a" : "#cf1322"}
            label="Service"
            value={
              status.data?.running ? (
                <Tag color="green">running</Tag>
              ) : (
                <Tag color="red">down</Tag>
              )
            }
            hint="crowdsec.service systemd unit state"
          />
          <MetricTile
            icon={<ApiOutlined />}
            tint={status.data?.lapi_reachable ? "#52c41a" : "#cf1322"}
            label="LAPI"
            value={
              status.data?.lapi_reachable ? (
                <Tag color="green">reachable</Tag>
              ) : (
                <Tag color="red">unreachable</Tag>
              )
            }
            hint="Local API socket reachable from panel-agent"
          />
          <MetricTile
            icon={<FileTextOutlined />}
            tint="#1677ff"
            label="Parsed events"
            value={metrics.data?.parsed ?? 0}
            hint="Log lines CrowdSec successfully parsed"
          />
          <MetricTile
            icon={<WarningOutlined />}
            tint="#faad14"
            label="Unparsed"
            value={metrics.data?.unparsed ?? 0}
            hint="Lines no parser matched (gaps in coverage)"
          />
          <MetricTile
            icon={<ThunderboltOutlined />}
            tint="#722ed1"
            label="Buckets fired"
            value={metrics.data?.buckets ?? 0}
            hint="Scenario thresholds tripped (suspicious patterns)"
          />
          <MetricTile
            icon={<SafetyOutlined />}
            tint="#cf1322"
            label="Active decisions"
            value={metrics.data?.decisions_active ?? 0}
            hint="IPs currently banned / under captcha"
          />
          <MetricTile
            icon={<BellOutlined />}
            tint="#13c2c2"
            label="Total alerts"
            value={metrics.data?.alerts_total ?? 0}
            hint="All-time alerts since CrowdSec started"
          />
          {status.data?.version && (
            <MetricTile
              icon={<AppstoreOutlined />}
              tint="#1677ff"
              label="Version"
              value={
                <Typography.Text code style={{ fontSize: 12 }}>
                  {status.data.version}
                </Typography.Text>
              }
              hint="Installed CrowdSec engine version"
            />
          )}
        </Row>
      )}

      <Alert
        type="info"
        showIcon
        message="What is CrowdSec?"
        description="Behaviour-based intrusion-prevention. Tails server logs (nginx, sshd, panel, mail), matches them against scenarios (brute-force, scanners, web exploits, credential stuffing), and emits IP decisions. Bouncers enforce them at the firewall (UFW), at nginx (AppSec WAF with OWASP CRS rules + optional captcha challenge), and against a crowdsourced blocklist of IPs flagged by the wider community in the last hours."
      />
      <CrowdsecTestIPCard />
    </Space>
  );

  const decisionsPanel = (
    <Card
      size="small"
      title="Active decisions"
      extra={
        <Space>
          <Select
            size="small"
            value={scope}
            style={{ minWidth: 160 }}
            options={SCOPE_OPTIONS}
            onChange={(v) => setScope(v)}
          />
          <Button type="primary" size="small" onClick={() => setAddOpen(true)}>
            Add decision
          </Button>
        </Space>
      }
    >
      <Table<CrowdsecDecision>
        rowKey="id"
        dataSource={decisions.data ?? []}
        loading={decisions.isLoading}
        pagination={{ pageSize: 20, showSizeChanger: false }}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No active decisions" /> }}
        scroll={{ x: "max-content" }}
      >
        <Table.Column<CrowdsecDecision> dataIndex="ip" title="IP" key="ip" />
        <Table.Column<CrowdsecDecision> dataIndex="scenario" title="Scenario" key="scenario" />
        <Table.Column<CrowdsecDecision> dataIndex="reason" title="Reason" key="reason" />
        <Table.Column<CrowdsecDecision>
          dataIndex="until"
          title="Until"
          key="until"
          render={(s: string) => fmtTime(s)}
        />
        <Table.Column<CrowdsecDecision>
          title=""
          key="delete"
          width={90}
          render={(_, row) => (
            <Popconfirm
              title="Remove ban"
              description={`Remove the ban on ${row.ip}? Traffic will resume immediately.`}
              okText="Remove"
              okButtonProps={{ danger: true }}
              cancelText="Cancel"
              onConfirm={() => onDeleteDecision(row)}
            >
              <RowActionButton danger size="small" icon={<DeleteOutlined />}>
                Delete
              </RowActionButton>
            </Popconfirm>
          )}
        />
      </Table>
    </Card>
  );

  const bouncersPanel = (
    <Card size="small" title="Bouncers">
      <Table
        rowKey="name"
        dataSource={bouncers.data ?? []}
        loading={bouncers.isLoading}
        pagination={false}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No bouncers registered" /> }}
        scroll={{ x: "max-content" }}
      >
        <Table.Column dataIndex="name" title="Name" key="name" />
        <Table.Column dataIndex="type" title="Type" key="type" />
        <Table.Column
          dataIndex="last_pull"
          title="Last pull"
          key="last_pull"
          render={(s: string, row: { name: string; type: string }) => {
            if (s) return fmtTime(s);
            const isAppSecOnly =
              row.type === "" ||
              row.name.toLowerCase().includes("nginx");
            if (isAppSecOnly) {
              return (
                <Tooltip title="AppSec-only bouncer (Lua HTTP). Doesn't poll LAPI for L3/L4 decisions — those are handled by cs-firewall-bouncer. Every nginx request is forwarded to the AppSec engine on 127.0.0.1:7422 instead.">
                  <Tag color="blue">AppSec-only</Tag>
                </Tooltip>
              );
            }
            return "—";
          }}
        />
        <Table.Column
          dataIndex="revoked"
          title="Status"
          key="revoked"
          render={(revoked: boolean) =>
            revoked ? <Tag color="red">revoked</Tag> : <Tag color="green">active</Tag>
          }
        />
      </Table>
    </Card>
  );

  const hubPanel = <RecommendedHubCard hub={hub} />;

  return (
    <>
      <Tabs
        activeKey={activeSub}
        onChange={onSubChange}
        // Force horizontal scroll on overflow instead of letting the bar
        // stretch beyond the card. Combined with the outer Security
        // page collapsing top-tab labels to icons under md, 390px
        // mobile shows the subtab bar scrollable without breaking
        // labels char-per-line.
        tabBarStyle={{ marginBottom: 12 }}
        size={isDesktop ? "middle" : "small"}
        items={[
          { key: "overview", label: "Overview", children: overviewPanel },
          { key: "hub", label: "Hub", children: hubPanel },
          { key: "decisions", label: "Active decisions", children: decisionsPanel },
          { key: "allowlist", label: "Allowlist", children: <AllowlistsCard /> },
          { key: "alerts", label: "Alerts", children: <AlertsCard /> },
          { key: "console", label: "Console", children: <ConsoleCard /> },
          { key: "captcha", label: "Captcha", children: <CaptchaRemediationCard /> },
          { key: "profiles", label: "Per-scenario", children: <ProfilesCard /> },
          { key: "appsec", label: "Block Country", children: <AppSecGeoblockCard /> },
          { key: "blocklists", label: "Blocklists", children: <BlocklistsCard /> },
          { key: "bouncers", label: "Bouncers", children: bouncersPanel },
        ]}
      />

      <Drawer
        title="Add CrowdSec decision (manual ban)"
        open={addOpen}
        onClose={() => setAddOpen(false)}
        width={isDesktop ? 520 : undefined}
        placement="right"
        destroyOnClose
        extra={
          <Space>
            <Button onClick={() => setAddOpen(false)}>Cancel</Button>
            <Button
              type="primary"
              danger
              loading={addDecision.isPending}
              onClick={() => addForm.submit()}
            >
              Add ban
            </Button>
          </Space>
        }
      >
        <Form<AddDecisionFormValues>
          form={addForm}
          layout="vertical"
          onFinish={submitAdd}
          initialValues={{ scope: "ip" }}
        >
          <Form.Item
            name="scope"
            label="Scope"
            rules={[{ required: true, message: "Scope required" }]}
            tooltip="Country bans rely on the GeoIP enricher; AS bans on the ASN enricher. Both are installed by default on fresh CrowdSec hosts."
          >
            <Select options={ADD_SCOPE_OPTIONS} />
          </Form.Item>
          <Form.Item
            noStyle
            shouldUpdate={(prev, next) => prev.scope !== next.scope}
          >
            {({ getFieldValue }) => {
              const s: CrowdsecScope = getFieldValue("scope") ?? "ip";
              const config: Record<
                CrowdsecScope,
                { label: string; placeholder: string; help: string; pattern: RegExp; msg: string }
              > = {
                ip: {
                  label: "IP address",
                  placeholder: "203.0.113.7",
                  help: "Single IPv4 or IPv6 address.",
                  pattern: IP_OR_CIDR,
                  msg: "Must be a valid IP address",
                },
                range: {
                  label: "CIDR range",
                  placeholder: "203.0.113.0/24",
                  help: "CIDR block. /24 is 256 addresses; /32 matches a single IP.",
                  pattern: IP_OR_CIDR,
                  msg: "Must be a valid CIDR (e.g. 203.0.113.0/24)",
                },
                country: {
                  label: "Country code",
                  placeholder: "IL",
                  help: "Two-letter ISO 3166-1 alpha-2 code (RU, CN, IR, …). Requires GeoIP enricher.",
                  pattern: COUNTRY_CODE,
                  msg: "Two-letter country code",
                },
                as: {
                  label: "ASN",
                  placeholder: "AS64500",
                  help: "Autonomous System number, with or without the AS prefix.",
                  pattern: ASN_RE,
                  msg: "ASN number (e.g. 64500 or AS64500)",
                },
              };
              const c = config[s];
              return (
                <Form.Item
                  name="value"
                  label={c.label}
                  extra={c.help}
                  rules={[
                    { required: true, message: `${c.label} required` },
                    { pattern: c.pattern, message: c.msg },
                  ]}
                >
                  <Input placeholder={c.placeholder} autoComplete="off" />
                </Form.Item>
              );
            }}
          </Form.Item>
          <Form.Item
            name="duration"
            label="Duration"
            initialValue="4h"
            rules={[
              { required: true, message: "Duration required" },
              { pattern: DURATION, message: 'Use Go duration syntax: "30m", "4h", "1h30m"' },
            ]}
          >
            <Input placeholder="4h" autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="reason"
            label="Reason"
            rules={[
              { required: true, message: "Reason required" },
              { min: 3, max: 200, message: "3..200 characters" },
            ]}
          >
            <Input placeholder="manual ban — repeated login abuse" autoComplete="off" />
          </Form.Item>
        </Form>
      </Drawer>
    </>
  );
};

// AppSecGeoblockCard — server-wide L7 country allow/deny list applied
// by CrowdSec AppSec's pre-evaluation hook (GeoIPEnrich(...).IsoCode).
// See https://doc.crowdsec.net/docs/next/appsec/rules_examples/#5-geoblocking.
// Unlike decisions (L3/L4 firewall-bouncer), this operates on HTTP
// requests reaching nginx + gets a 403 with a DropRequest("Forbidden
// Country") reason. Operator must wire nginx to CrowdSec's AppSec
// endpoint for enforcement — see plans/m26-security-tab-runbook.md.
const AppSecGeoblockCard = () => {
  const geoblock = useAppSecGeoblock();
  const updateGeoblock = useUpdateAppSecGeoblock();

  const [mode, setMode] = useState<AppSecGeoblockMode>("off");
  const [countries, setCountries] = useState<string[]>([]);

  // Pre-built option set for the country Select. Memoised once at module
  // load — ISO3166_COUNTRIES is a frozen literal so the .map is cheap
  // either way, but useMemo here keeps Select.options stable across
  // renders (helps AntD virtualisation cache).
  const countryOptions = useMemo(
    () =>
      ISO3166_COUNTRIES.map((c) => ({
        value: c.code,
        label: `${c.flag}  ${c.name} (${c.code})`,
        searchKey: `${c.name} ${c.code}`.toLowerCase(),
      })),
    [],
  );

  useEffect(() => {
    if (geoblock.data) {
      setMode(geoblock.data.mode);
      setCountries(geoblock.data.countries);
    }
  }, [geoblock.data]);

  const dirty =
    geoblock.data !== undefined &&
    (mode !== geoblock.data.mode ||
      countries.join(",") !== geoblock.data.countries.join(","));

  const apply = async () => {
    try {
      await updateGeoblock.mutateAsync({ mode, countries });
      message.success("AppSec geoblock updated and crowdsec reloaded");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to apply geoblock");
    }
  };

  return (
    <Card size="small" title="AppSec geoblock (server-wide)" loading={geoblock.isLoading}>
      <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          HTTP-layer country filter applied by CrowdSec AppSec. Blocks with
          403 at the nginx edge; complementary to IP/range bans (L3). Needs
          the GeoIP enricher and nginx → AppSec wiring — see runbook.
        </Typography.Paragraph>
        <div>
          <Typography.Text strong>Mode: </Typography.Text>
          <Radio.Group value={mode} onChange={(e) => setMode(e.target.value)}>
            <Radio.Button value="off">Off</Radio.Button>
            <Radio.Button value="allow">Allow-list</Radio.Button>
            <Radio.Button value="deny">Deny-list</Radio.Button>
          </Radio.Group>
        </div>
        {mode !== "off" && (
          <div>
            <Typography.Text strong>Countries: </Typography.Text>
            <Select<string[]>
              mode="multiple"
              style={{ width: "100%", maxWidth: 720 }}
              placeholder="Type a country name or code, or pick from the list"
              value={countries}
              onChange={(next) =>
                setCountries(
                  next
                    .map((c) => c.toUpperCase().trim())
                    .filter((c) => /^[A-Z]{2}$/.test(c)),
                )
              }
              options={countryOptions}
              showSearch
              optionFilterProp="searchKey"
              filterOption={(input, opt) =>
                (opt?.searchKey ?? "").includes(input.toLowerCase())
              }
              maxTagCount="responsive"
              allowClear
            />
          </div>
        )}
        {mode === "allow" && countries.length === 0 && (
          <Alert
            type="warning"
            showIcon
            message="Allow-list with no countries blocks every request — add at least one before applying."
          />
        )}
        {mode === "deny" && countries.length === 0 && (
          <Alert
            type="warning"
            showIcon
            message="Deny-list with no countries has no effect — add at least one before applying."
          />
        )}
        <Space>
          <Popconfirm
            title="Apply AppSec geoblock"
            description={
              mode === "off"
                ? "Disables the server-wide country filter. Requests from any country pass AppSec."
                : `${mode === "allow" ? "Allow-list" : "Deny-list"} mode with ${countries.length} ${
                    countries.length === 1 ? "country" : "countries"
                  }. CrowdSec is reloaded (SIGHUP) — no traffic drops.`
            }
            okText="Apply"
            onConfirm={apply}
            disabled={!dirty || updateGeoblock.isPending}
          >
            <Button
              type="primary"
              disabled={!dirty}
              loading={updateGeoblock.isPending}
            >
              Apply
            </Button>
          </Popconfirm>
          {dirty && (
            <Button
              onClick={() => {
                if (geoblock.data) {
                  setMode(geoblock.data.mode);
                  setCountries(geoblock.data.countries);
                }
              }}
            >
              Reset
            </Button>
          )}
        </Space>
      </Space>
    </Card>
  );
};

// AllowlistsCard — server-wide IP/CIDR never-ban list (M27 Step 2,
// ADR-0061). LAPI is truth; jabali shells to cscli via the agent. Table
// + Drawer-for-add follows docs/CONVENTIONS.md — Drawer not Modal, `Table.Column`
// children, `destroyOnClose`.
type AllowlistFormValues = {
  value: string;
  reason: string;
};

const ALLOWLIST_IP_OR_CIDR = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/;

// BlocklistsCard — community blocklists currently contributing active
// decisions to this engine. Subscriptions live at app.crowdsec.net.
const BlocklistsCard = () => {
  const q = useCrowdsecBlocklists();
  const refresh = useRefreshCrowdsecBlocklists();
  const data = q.data?.blocklists ?? [];
  const total = q.data?.total ?? 0;

  return (
    <Card
      title="Active decision sources"
      extra={
        <Space size={12}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Total blocked: {total.toLocaleString()}
          </Typography.Text>
          <Button
            size="small"
            icon={<ReloadOutlined />}
            loading={refresh.isPending}
            onClick={() => refresh.mutate()}
          >
            Refresh
          </Button>
        </Space>
      }
    >
      <Alert
        type="info"
        showIcon
        message="Aggregates every active decision by (origin/scenario). CAPI = pulled from app.crowdsec.net; cscli-import = manually imported; crowdsec = local detection. Subscribe to community blocklists at app.crowdsec.net → Blocklists."
        style={{ marginBottom: 12 }}
      />
      <Table<{ name: string; count: number; latest_end: string }>
        size="small"
        loading={q.isPending}
        rowKey="name"
        dataSource={data}
        pagination={false}
        locale={{ emptyText: "No active decisions on this engine yet." }}
        scroll={{ x: "max-content" }}
        columns={[
          {
            title: "Blocklist",
            dataIndex: "name",
            key: "name",
            render: (v: string) => <Typography.Text code>{v}</Typography.Text>,
          },
          {
            title: "Active decisions",
            dataIndex: "count",
            key: "count",
            align: "right",
            render: (v: number) => v.toLocaleString(),
          },
          {
            title: "Latest expiry",
            dataIndex: "latest_end",
            key: "latest_end",
            render: (v: string) => (v ? new Date(v).toLocaleString() : "—"),
          },
        ]}
      />
    </Card>
  );
};

const AllowlistsCard = () => {
  const allowlists = useCrowdsecAllowlists();
  const addEntry = useAddCrowdsecAllowlist();
  const removeEntry = useRemoveCrowdsecAllowlist();
  const [addOpen, setAddOpen] = useState(false);
  const [form] = Form.useForm<AllowlistFormValues>();
  const screens = Grid.useBreakpoint();
  const isDesktop = screens.lg ?? (typeof window !== "undefined" ? window.innerWidth >= 992 : true);

  const onSubmit = async (values: AllowlistFormValues) => {
    try {
      await addEntry.mutateAsync(values);
      message.success(`Allowlisted ${values.value}`);
      setAddOpen(false);
      form.resetFields();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to allowlist");
    }
  };

  const onRemove = async (row: CrowdsecAllowlistEntry) => {
    try {
      await removeEntry.mutateAsync(row.value);
      message.success(`Removed ${row.value} from allowlist`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Failed to remove");
    }
  };

  return (
    <>
      <Card
        size="small"
        title="Allowlist (never ban)"
        extra={
          <Button type="primary" size="small" onClick={() => setAddOpen(true)}>
            Add to allowlist
          </Button>
        }
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="Allowlisted IPs bypass every scenario, decision, and the AppSec geoblock. Use for your office, home IP, or CI runner CIDR."
        />
        <Table<CrowdsecAllowlistEntry>
          rowKey="value"
          dataSource={allowlists.data ?? []}
          loading={allowlists.isLoading}
          pagination={{ pageSize: 10, showSizeChanger: false }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No allowlist entries" /> }}
          scroll={{ x: "max-content" }}
        >
          <Table.Column<CrowdsecAllowlistEntry>
            dataIndex="value"
            title="IP or CIDR"
            key="value"
            render={(v: string) => <Typography.Text code>{v}</Typography.Text>}
          />
          <Table.Column<CrowdsecAllowlistEntry>
            dataIndex="reason"
            title="Reason"
            key="reason"
          />
          <Table.Column<CrowdsecAllowlistEntry>
            dataIndex="created_at"
            title="Added"
            key="created_at"
            render={(s: string) => fmtTime(s)}
          />
          <Table.Column<CrowdsecAllowlistEntry>
            title=""
            key="delete"
            width={90}
            render={(_, row) => (
              <Popconfirm
                title="Remove from allowlist"
                description={`${row.value} will be subject to scenarios and decisions again.`}
                okText="Remove"
                okButtonProps={{ danger: true }}
                cancelText="Cancel"
                onConfirm={() => onRemove(row)}
              >
                <Button danger type="text" size="small">
                  Remove
                </Button>
              </Popconfirm>
            )}
          />
        </Table>
      </Card>

      <Drawer
        title="Add to allowlist"
        open={addOpen}
        onClose={() => setAddOpen(false)}
        width={isDesktop ? 520 : undefined}
        placement="right"
        destroyOnClose
        extra={
          <Space>
            <Button onClick={() => setAddOpen(false)}>Cancel</Button>
            <Button type="primary" loading={addEntry.isPending} onClick={() => form.submit()}>
              Add
            </Button>
          </Space>
        }
      >
        <Form<AllowlistFormValues> form={form} layout="vertical" onFinish={onSubmit}>
          <Form.Item
            name="value"
            label="IP or CIDR"
            rules={[
              { required: true, message: "Value required" },
              { pattern: ALLOWLIST_IP_OR_CIDR, message: "Must be a valid IP or CIDR" },
            ]}
            tooltip="Single IPv4/IPv6 address or CIDR block (e.g. 192.0.2.1 or 10.0.0.0/24)."
          >
            <Input placeholder="192.0.2.1 or 10.0.0.0/24" />
          </Form.Item>
          <Form.Item
            name="reason"
            label="Reason"
            rules={[
              { required: true, message: "Reason required" },
              { min: 3, max: 200, message: "Reason must be 3..200 chars" },
            ]}
          >
            <Input placeholder="e.g. office LAN, CI runner" />
          </Form.Item>
        </Form>
      </Drawer>
    </>
  );
};

// AlertsCard — read-only list of CrowdSec scenario fires. Row click
// opens a Drawer with the full alert detail (events + decisions). No
// mutations; upstream caps to 100/24h server-side (M27 Step 3).
type AlertDetail = {
  id?: number;
  scenario?: string;
  source?: { ip?: string; scope?: string; value?: string; cn?: string; as_name?: string };
  events?: Array<{ timestamp?: string; meta?: Array<{ key: string; value: string }> }>;
  decisions?: Array<{ type?: string; value?: string; duration?: string; scenario?: string }>;
  start_at?: string;
  stop_at?: string;
  events_count?: number;
  machine_id?: string;
};

const AlertsCard = () => {
  const alerts = useCrowdsecAlerts();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const detail = useCrowdsecAlert(selectedId);
  const alert = detail.data as AlertDetail | undefined;

  return (
    <>
      <Card size="small" title="Alerts (last 24h)">
        <Table<CrowdsecAlert>
          rowKey="id"
          dataSource={alerts.data ?? []}
          loading={alerts.isLoading}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No alerts in the last 24h" /> }}
          scroll={{ x: "max-content" }}
          onRow={(row) => ({
            onClick: () => setSelectedId(row.id),
            style: { cursor: "pointer" },
          })}
        >
          <Table.Column<CrowdsecAlert> dataIndex="scenario" title="Scenario" key="scenario" />
          <Table.Column<CrowdsecAlert>
            title="Source"
            key="source"
            render={(_, row) => (
              <Space size="small">
                <Typography.Text code>{row.source_ip || row.source_value || "—"}</Typography.Text>
                {row.source_scope && <Tag>{row.source_scope}</Tag>}
              </Space>
            )}
          />
          <Table.Column<CrowdsecAlert>
            dataIndex="events_count"
            title="Events"
            key="events_count"
            width={100}
          />
          <Table.Column<CrowdsecAlert>
            dataIndex="decisions_count"
            title="Decisions"
            key="decisions_count"
            width={110}
          />
          <Table.Column<CrowdsecAlert>
            dataIndex="started_at"
            title="Started"
            key="started_at"
            render={(s: string) => fmtTime(s)}
          />
          <Table.Column<CrowdsecAlert>
            dataIndex="machine_id"
            title="Machine"
            key="machine_id"
            render={(s: string) => <Typography.Text type="secondary">{s || "—"}</Typography.Text>}
          />
        </Table>
      </Card>

      <Drawer
        title={alert?.scenario ? `Alert: ${alert.scenario}` : "Alert detail"}
        open={selectedId !== null}
        onClose={() => setSelectedId(null)}
        width={720}
        placement="right"
        destroyOnClose
      >
        {detail.isLoading ? (
          <Typography.Text type="secondary">Loading…</Typography.Text>
        ) : alert ? (
          <Space direction="vertical" size="large" style={{ width: "100%" }}>
            <Descriptions
              column={1}
              size="small"
              items={[
                { key: "scenario", label: "Scenario", children: alert.scenario ?? "—" },
                {
                  key: "source",
                  label: "Source",
                  children: (
                    <Space size="small" wrap>
                      <Typography.Text code>{alert.source?.ip ?? alert.source?.value ?? "—"}</Typography.Text>
                      {alert.source?.scope && <Tag>{alert.source.scope}</Tag>}
                      {alert.source?.cn && <Tag color="blue">{alert.source.cn}</Tag>}
                    </Space>
                  ),
                },
                { key: "events", label: "Events count", children: String(alert.events_count ?? 0) },
                { key: "start", label: "Started", children: fmtTime(alert.start_at) },
                { key: "stop", label: "Stopped", children: fmtTime(alert.stop_at) },
                { key: "machine", label: "Machine", children: alert.machine_id ?? "—" },
              ]}
            />

            <Card size="small" title="Decisions issued">
              {(alert.decisions ?? []).length === 0 ? (
                <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No decisions issued" />
              ) : (
                <Table
                  rowKey={(_, idx) => `d-${idx}`}
                  dataSource={alert.decisions}
                  pagination={false}
                  size="small"
                  scroll={{ x: "max-content" }}
                >
                  <Table.Column dataIndex="type" title="Type" key="type" />
                  <Table.Column dataIndex="value" title="Value" key="value" />
                  <Table.Column dataIndex="duration" title="Duration" key="duration" />
                </Table>
              )}
            </Card>
          </Space>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Alert not found" />
        )}
      </Drawer>
    </>
  );
};

// ConsoleCard — CrowdSec Console enrollment (M27 Step 4, ADR-0062).
// One-shot enroll form; no status polling, no disenroll. See ADR for
// why — cscli has no disenroll verb and enrollment state isn't
// distinguishable from config files.
type ConsoleFormValues = {
  key: string;
  name?: string;
};

const OPTION_LABEL: Record<string, string> = {
  custom: "Custom scenarios",
  manual: "Manual decisions",
  tainted: "Tainted scenarios",
  context: "Context with alerts",
  console_management: "Console-managed decisions",
};

const OPTION_TOOLTIP: Record<string, string> = {
  custom:
    "Your own detection scenarios not from the CrowdSec Hub. Sharing helps CrowdSec improve detection, but exposes your private rule logic to the Console.",
  manual:
    "Bans and unbans you added by hand (e.g. via cscli decisions add). Forwarding makes them visible in the Console dashboard.",
  tainted:
    "Hub scenarios you have locally modified. CrowdSec marks these 'tainted' because they differ from the official version. Enable only if your modified logic is not sensitive.",
  context:
    "Extra metadata attached to each alert — such as HTTP paths, User-Agent strings, or POST body fragments. Useful for threat analysis but may contain PII; enable only if acceptable under your privacy policy.",
  console_management:
    "Allow the Console to push block and captcha decisions to this engine remotely. Required for centralised blocklist management from app.crowdsec.net.",
};

const ConsoleCard = () => {
  const enroll = useEnrollCrowdsecConsole();
  const disenroll = useDisenrollCrowdsecConsole();
  const enrollmentQ = useCrowdsecConsoleEnrollment();
  const statusQ = useCrowdsecConsoleStatus();
  const toggle = useToggleCrowdsecConsoleOption();
  const [form] = Form.useForm<ConsoleFormValues>();
  const [submittedAt, setSubmittedAt] = useState<number | null>(null);

  const isEnrolled = enrollmentQ.data?.enrolled === true;

  const onSubmit = async (values: ConsoleFormValues) => {
    try {
      await enroll.mutateAsync(values);
      setSubmittedAt(Date.now());
      form.resetFields();
      message.success("Enrollment sent — accept this instance at app.crowdsec.net");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Enrollment failed");
    }
  };

  const onDisenroll = async () => {
    try {
      await disenroll.mutateAsync();
      message.success("Disenrolled — you can enroll with a new key now");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Disenroll failed");
    }
  };

  const onToggleOption = async (opt: CrowdsecConsoleOption, enabled: boolean) => {
    try {
      await toggle.mutateAsync({ option: opt.name, enabled });
      message.success(`${enabled ? "Enabled" : "Disabled"} ${OPTION_LABEL[opt.name] ?? opt.name}`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Toggle failed");
    }
  };

  return (
    <Card
      size="small"
      title="CrowdSec Console (optional)"
      extra={
        <Typography.Link
          href={isEnrolled ? "https://app.crowdsec.net/security-engines" : "https://app.crowdsec.net/security-engines?distribution=linux"}
          target="_blank"
          rel="noopener noreferrer"
        >
          {isEnrolled ? "Manage at app.crowdsec.net" : "Get enrollment key"}
        </Typography.Link>
      }
    >
      <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        {isEnrolled ? (
          <>
            <Alert
              type="success"
              showIcon
              message={
                <>
                  Enrolled as <Typography.Text code>{enrollmentQ.data?.login}</Typography.Text>
                  {enrollmentQ.data?.capi_ok === false && (
                    <Tag color="orange" style={{ marginLeft: 8 }}>
                      CAPI auth failing
                    </Tag>
                  )}
                </>
              }
              description={
                <>
                  This engine is registered with the CrowdSec Console. CTI community blocklist
                  pulls, hosted dashboards, and remote management are active. Use{" "}
                  <strong>Disenroll</strong> below to drop the registration so you can re-enroll
                  with a different key.
                </>
              }
            />
            <Space>
              <Popconfirm
                title="Disenroll this instance?"
                description="Removes /etc/crowdsec/online_api_credentials.yaml and reloads crowdsec. Community blocklist pulls + Console dashboards stop until you enroll again."
                okText="Disenroll"
                okButtonProps={{ danger: true }}
                cancelText="Cancel"
                onConfirm={onDisenroll}
              >
                <Button danger loading={disenroll.isPending}>
                  Disenroll
                </Button>
              </Popconfirm>
            </Space>
          </>
        ) : (
          <>
            <Alert
              type="info"
              showIcon
              message="Enroll this instance to receive CTI community blocklists and a hosted dashboard."
              description={
                <>
                  Open{" "}
                  <Typography.Link
                    href="https://app.crowdsec.net/security-engines?distribution=linux"
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    app.crowdsec.net/security-engines
                  </Typography.Link>
                  , copy the <Typography.Text code>cscli console enroll &lt;key&gt;</Typography.Text>{" "}
                  command, paste the key below, then accept the pending instance in the Console.
                  Share settings and disenroll are managed in the Console web UI.
                </>
              }
            />

            {submittedAt !== null && (
              <Alert
                type="success"
                showIcon
                message="Enrollment command sent"
                description="Visit app.crowdsec.net and accept this instance. It can take up to a minute to appear."
              />
            )}

            <Form<ConsoleFormValues> form={form} layout="vertical" onFinish={onSubmit}>
              <Form.Item
                name="key"
                label="Enrollment key"
                rules={[
                  { required: true, message: "Key required" },
                  { pattern: /^[A-Za-z0-9-]{16,128}$/, message: "16-128 alnum + dash chars" },
                ]}
              >
                <Input.Password
                  placeholder="cskf-xxxxxxxxxxxxxxxxxxxx"
                  autoComplete="off"
                  visibilityToggle={false}
                />
              </Form.Item>
              <Form.Item
                name="name"
                label="Instance name (optional)"
                tooltip="Display name in the Console dashboard. Defaults to the server hostname."
              >
                <Input placeholder={`e.g. jabali-prod-${new Date().getFullYear()}`} maxLength={64} />
              </Form.Item>
              <Space>
                <Popconfirm
                  title="Enroll this instance?"
                  description="Scenario fires, decisions, and alerts will be shared with CrowdSec Console per your sharing settings."
                  okText="Enroll"
                  cancelText="Cancel"
                  onConfirm={() => form.submit()}
                >
                  <Button type="primary" loading={enroll.isPending}>
                    Enroll
                  </Button>
                </Popconfirm>
              </Space>
            </Form>
          </>
        )}

        <Typography.Title level={5} style={{ marginTop: 16, marginBottom: 8 }}>
          Share preferences
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
          Controls which data the instance forwards to Console. Takes effect only after enrollment
          is accepted at app.crowdsec.net.
        </Typography.Paragraph>
        <Table<CrowdsecConsoleOption>
          rowKey="name"
          dataSource={statusQ.data ?? []}
          loading={statusQ.isLoading}
          pagination={false}
          size="small"
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Console options unavailable" /> }}
          scroll={{ x: "max-content" }}
        >
          <Table.Column<CrowdsecConsoleOption>
            dataIndex="name"
            title="Option"
            key="name"
            render={(n: string) => (
              <Space size={6}>
                {OPTION_LABEL[n] ?? n}
                {OPTION_TOOLTIP[n] && (
                  <Tooltip title={OPTION_TOOLTIP[n]}>
                    <span style={{ color: "var(--ant-color-text-secondary)", cursor: "help" }}>
                      <QuestionCircleOutlined />
                    </span>
                  </Tooltip>
                )}
              </Space>
            )}
          />
          <Table.Column<CrowdsecConsoleOption>
            dataIndex="description"
            title="Description"
            key="description"
          />
          <Table.Column<CrowdsecConsoleOption>
            dataIndex="enabled"
            title="Enabled"
            key="enabled"
            width={120}
            render={(enabled: boolean, row) => (
              <Switch
                checked={enabled}
                loading={toggle.isPending}
                onChange={(checked) => onToggleOption(row, checked)}
              />
            )}
          />
        </Table>
      </Space>
    </Card>
  );
};

// CaptchaRemediationCard — hCaptcha / reCAPTCHA / Turnstile credentials
// for crowdsec-nginx-bouncer (M27 Step 5). Secret is write-only —
// GET never returns it; empty secret on PUT means "keep existing."
type CaptchaFormValues = {
  enabled: boolean;
  provider: CrowdsecCaptchaProvider;
  site_key: string;
  secret_key: string;
};

const PROVIDER_OPTIONS = [
  { value: "hcaptcha", label: "hCaptcha" },
  { value: "recaptcha", label: "reCAPTCHA v2" },
  { value: "turnstile", label: "Cloudflare Turnstile" },
];

const CaptchaRemediationCard = () => {
  const captcha = useCrowdsecCaptcha();
  const update = useUpdateCrowdsecCaptcha();
  const [form] = Form.useForm<CaptchaFormValues>();

  useEffect(() => {
    if (captcha.data) {
      form.setFieldsValue({
        enabled: captcha.data.enabled,
        provider: captcha.data.provider || "hcaptcha",
        site_key: captcha.data.site_key,
        secret_key: "",
      });
    }
  }, [captcha.data, form]);

  const onSubmit = async (values: CaptchaFormValues) => {
    try {
      await update.mutateAsync({
        enabled: values.enabled,
        provider: values.enabled ? values.provider : "",
        site_key: values.site_key,
        secret_key: values.secret_key, // "" = keep existing
      });
      form.setFieldValue("secret_key", "");
      message.success("Captcha settings saved");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Save failed");
    }
  };

  return (
    <Card
      size="small"
      title="Captcha remediation"
      loading={captcha.isLoading}
      extra={
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          Requires nginx-bouncer (installed with CrowdSec)
        </Typography.Text>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="When enabled, CrowdSec scenarios flagged for captcha serve a challenge page instead of a 403."
        description="Create an hCaptcha / reCAPTCHA / Turnstile site at the provider, paste the keys below. Secret is stored server-side and never returned."
      />
      <Form<CaptchaFormValues>
        form={form}
        layout="vertical"
        onFinish={onSubmit}
        initialValues={{ enabled: false, provider: "hcaptcha", site_key: "", secret_key: "" }}
      >
        <Form.Item name="enabled" label="Enabled" valuePropName="checked">
          <Radio.Group
            options={[
              { value: false, label: "Off" },
              { value: true, label: "On" },
            ]}
            optionType="button"
          />
        </Form.Item>
        <Form.Item
          noStyle
          shouldUpdate={(prev, next) => prev.enabled !== next.enabled}
        >
          {({ getFieldValue }) => {
            const enabled = getFieldValue("enabled") as boolean;
            return (
              <>
                <Form.Item
                  name="provider"
                  label="Provider"
                  rules={enabled ? [{ required: true, message: "Provider required" }] : []}
                >
                  <Select disabled={!enabled} options={PROVIDER_OPTIONS} />
                </Form.Item>
                <Form.Item
                  name="site_key"
                  label="Site key (public)"
                  rules={
                    enabled
                      ? [{ required: true, message: "Site key required" }, { max: 512 }]
                      : []
                  }
                >
                  <Input disabled={!enabled} placeholder="publishable site key" />
                </Form.Item>
                <Form.Item
                  name="secret_key"
                  label="Secret key (write-only)"
                  tooltip="Leave blank to keep the stored secret unchanged."
                  rules={[{ max: 512 }]}
                >
                  <Input.Password
                    disabled={!enabled}
                    placeholder={captcha.data?.enabled ? "(unchanged)" : "secret key"}
                    autoComplete="off"
                    visibilityToggle={false}
                  />
                </Form.Item>
              </>
            );
          }}
        </Form.Item>
        <Space>
          <Popconfirm
            title="Apply captcha settings?"
            description="This rewrites /etc/crowdsec/bouncers/crowdsec-nginx-bouncer.conf and reloads nginx."
            okText="Apply"
            cancelText="Cancel"
            onConfirm={() => form.submit()}
          >
            <Button type="primary" loading={update.isPending}>
              Save
            </Button>
          </Popconfirm>
        </Space>
      </Form>
    </Card>
  );
};

// ProfilesCard — per-scenario remediation override (M27 Step 6, ADR-0063).
// Row-per-scenario; inline Select for default/captcha/off. Captcha option
// greyed out when captcha_enabled=false (requires Step 5 configured).
type ProfileRow = CrowdsecScenarioItem & {
  override: "default" | "captcha" | "off";
};

const ProfilesCard = () => {
  const profiles = useCrowdsecProfiles();
  const update = useUpdateCrowdsecProfiles();
  const [draft, setDraft] = useState<Record<string, "default" | "captcha" | "off">>({});

  const rows: ProfileRow[] = (profiles.data?.scenarios ?? []).map((s) => {
    const existing = (profiles.data?.overrides ?? []).find((o) => o.scenario === s.name);
    const fromServer = (existing?.action ?? "default") as ProfileRow["override"];
    const override = draft[s.name] ?? fromServer;
    return { ...s, override };
  });

  const dirty = rows.some((r) => {
    const existing = (profiles.data?.overrides ?? []).find((o) => o.scenario === r.name);
    const fromServer = (existing?.action ?? "default") as ProfileRow["override"];
    return r.override !== fromServer;
  });

  const captchaEnabled = profiles.data?.captcha_enabled ?? false;

  const onApply = async () => {
    const overrides: CrowdsecProfileOverride[] = rows
      .filter((r) => r.override !== "default")
      .map((r) => ({ scenario: r.name, action: r.override as "captcha" | "off" }));
    try {
      await update.mutateAsync(overrides);
      setDraft({});
      message.success("Profiles saved — crowdsec reloaded");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "Save failed");
    }
  };

  return (
    <Card
      size="small"
      title="Per-scenario remediation override"
      loading={profiles.isLoading}
      extra={
        dirty && (
          <Space>
            <Button onClick={() => setDraft({})}>Reset</Button>
            <Popconfirm
              title="Apply overrides?"
              description="Rewrites /etc/crowdsec/profiles.yaml (marker-bounded) and reloads crowdsec."
              okText="Apply"
              cancelText="Cancel"
              onConfirm={onApply}
            >
              <Button type="primary" loading={update.isPending}>
                Apply
              </Button>
            </Popconfirm>
          </Space>
        )
      }
    >
      {!captchaEnabled && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="Captcha action requires the Captcha remediation card above to be enabled."
        />
      )}
      <Table<ProfileRow>
        rowKey="name"
        dataSource={rows}
        pagination={{ pageSize: 20, showSizeChanger: false }}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No scenarios installed" /> }}
        tableLayout="fixed"
      >
        <Table.Column<ProfileRow>
          dataIndex="name"
          title="Scenario"
          key="name"
          width={320}
          render={(v: string) => <Typography.Text code>{v}</Typography.Text>}
        />
        <Table.Column<ProfileRow>
          dataIndex="description"
          title="Description"
          key="description"
          ellipsis={{ showTitle: false }}
          render={(v: string) => (
            <Tooltip title={v} placement="topLeft">
              <span>{v}</span>
            </Tooltip>
          )}
        />
        <Table.Column<ProfileRow>
          title="Override"
          key="override"
          width={200}
          render={(_, row) => (
            <Select
              size="small"
              style={{ width: 170 }}
              value={row.override}
              onChange={(v) => setDraft((d) => ({ ...d, [row.name]: v }))}
              options={[
                { value: "default", label: "Default (ban)" },
                { value: "captcha", label: "Captcha", disabled: !captchaEnabled },
                { value: "off", label: "Off (bypass)" },
              ]}
            />
          )}
        />
      </Table>
    </Card>
  );
};

// RecommendedHubCard — curated picker of well-known free CrowdSec Hub
// items. Each entry maps to `cscli <type> install <name>`. Catalog is
// hand-maintained because cscli has no "free vs premium" filter and the
// upstream Console catalog (Premium/Enterprise blocklists) requires a
// signed-in account; everything below works on a fresh install with no
// enrollment.
type RecommendedItem = {
  type: "collections" | "scenarios" | "parsers" | "appsec-rules";
  name: string;
  title: string;
  description: string;
  category: "core" | "web" | "appsec" | "intel";
};

const RECOMMENDED_HUB_ITEMS: RecommendedItem[] = [
  {
    type: "collections",
    name: "crowdsecurity/linux",
    title: "Linux base",
    description: "syslog, sshd, journald — required base for almost every other collection",
    category: "core",
  },
  {
    type: "collections",
    name: "crowdsecurity/sshd",
    title: "SSH brute-force",
    description: "Detects sshd password brute-force, key rejection floods, slow-rate attacks",
    category: "core",
  },
  {
    type: "collections",
    name: "crowdsecurity/nginx",
    title: "nginx (web access)",
    description: "Parsers + scenarios for nginx access/error logs (HTTP scans, bad-bots, 4xx floods)",
    category: "web",
  },
  {
    type: "collections",
    name: "crowdsecurity/base-http-scenarios",
    title: "Generic HTTP scenarios",
    description: "Crawl detection, path traversal probes, generic HTTP exploits (works with any web server)",
    category: "web",
  },
  {
    type: "collections",
    name: "crowdsecurity/http-cve",
    title: "HTTP CVE detection",
    description: "Known-CVE exploit fingerprints (Log4Shell, Spring4Shell, CVE-2023-* WordPress CVEs)",
    category: "web",
  },
  {
    type: "collections",
    name: "crowdsecurity/wordpress",
    title: "WordPress",
    description: "wp-login brute force, xmlrpc abuse, plugin/theme CVE exploits",
    category: "web",
  },
  {
    type: "collections",
    name: "crowdsecurity/whitelist-good-actors",
    title: "Good-actor whitelist",
    description: "Skip bans for googlebot/bingbot/cloudflare/AWS health probes — reduces false positives",
    category: "intel",
  },
  {
    type: "collections",
    name: "crowdsecurity/appsec-virtual-patching",
    title: "AppSec virtual patching",
    description: "Pre-eval AppSec rules for unpatched CVEs (blocks the request, not the IP). Already shipped by jabali — install adds upstream updates",
    category: "appsec",
  },
  {
    type: "collections",
    name: "crowdsecurity/appsec-generic-rules",
    title: "AppSec generic rules",
    description: "Generic CRS-style patterns (XSS, SQLi, RCE) for nginx-bouncer in-band filtering",
    category: "appsec",
  },
];

const CATEGORY_COLOR: Record<RecommendedItem["category"], string> = {
  core: "geekblue",
  web: "blue",
  appsec: "magenta",
  intel: "green",
};

const RecommendedHubCard = ({
  hub,
}: {
  hub: ReturnType<typeof useCrowdsecHub>;
}) => {
  const install = useInstallCrowdsecHubItem();
  const remove = useRemoveCrowdsecHubItem();
  const [pending, setPending] = useState<string | null>(null);

  // Index installed items by `<type>:<name>` for O(1) lookup.
  const installedKey = useMemo(() => {
    const set = new Set<string>();
    (hub.data ?? []).forEach((it) => {
      if (it.installed) set.add(`${it.type}:${it.name}`);
    });
    return set;
  }, [hub.data]);

  const onInstall = async (item: RecommendedItem) => {
    setPending(`${item.type}:${item.name}`);
    try {
      await install.mutateAsync({ type: item.type, name: item.name });
      message.success(`Installed ${item.name}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Install failed");
    } finally {
      setPending(null);
    }
  };

  const onRemove = async (item: RecommendedItem) => {
    setPending(`${item.type}:${item.name}`);
    try {
      await remove.mutateAsync({ type: item.type, name: item.name });
      message.success(`Removed ${item.name}`);
    } catch (err) {
      message.error(err instanceof Error ? err.message : "Remove failed");
    } finally {
      setPending(null);
    }
  };

  return (
    <Card
      size="small"
      title="Recommended free blocklists & scenarios"
      extra={
        <Typography.Link
          href="https://hub.crowdsec.net/"
          target="_blank"
          rel="noopener noreferrer"
        >
          hub.crowdsec.net
        </Typography.Link>
      }
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="One-click install of upstream CrowdSec Hub items"
        description={
          <>
            Curated free items from the public Hub — no Console enrollment required. Install runs{" "}
            <Typography.Text code>cscli &lt;type&gt; install &lt;name&gt;</Typography.Text> and reloads
            crowdsec. Premium/Enterprise blocklists (firehol, dshield, etc.) need an account and are
            managed in the Console web UI after enrollment.
          </>
        }
      />
      <Table<RecommendedItem>
        rowKey={(r) => `${r.type}:${r.name}`}
        dataSource={RECOMMENDED_HUB_ITEMS}
        pagination={false}
        size="small"
        tableLayout="fixed"
      >
        <Table.Column<RecommendedItem>
          title="Item"
          key="title"
          width={260}
          render={(_, row) => (
            <Space direction="vertical" size={0}>
              <Space size={6} wrap>
                <Typography.Text strong>{row.title}</Typography.Text>
                <Tag color={CATEGORY_COLOR[row.category]}>{row.category}</Tag>
                {installedKey.has(`${row.type}:${row.name}`) && (
                  <Tag color="green">installed</Tag>
                )}
              </Space>
              <Typography.Text code style={{ fontSize: 12 }}>
                {row.name}
              </Typography.Text>
            </Space>
          )}
        />
        <Table.Column<RecommendedItem>
          title="Description"
          dataIndex="description"
          key="description"
          ellipsis={{ showTitle: false }}
          render={(v: string) => (
            <Tooltip title={v} placement="topLeft">
              <Typography.Text type="secondary">{v}</Typography.Text>
            </Tooltip>
          )}
        />
        <Table.Column<RecommendedItem>
          title=""
          key="action"
          width={130}
          align="right"
          render={(_, row) => {
            const key = `${row.type}:${row.name}`;
            const isInstalled = installedKey.has(key);
            const busy = pending === key;
            return isInstalled ? (
              <Popconfirm
                title={`Remove ${row.name}?`}
                okText="Remove"
                okButtonProps={{ danger: true }}
                onConfirm={() => onRemove(row)}
              >
                <Button size="small" danger loading={busy}>
                  Remove
                </Button>
              </Popconfirm>
            ) : (
              <Button size="small" type="primary" loading={busy} onClick={() => onInstall(row)}>
                Install
              </Button>
            );
          }}
        />
      </Table>
    </Card>
  );
};
