// Shared settings modal for nginx custom directives used by both admin and user domain lists.
// Opens a modal with tabs: "Rule Builder" and "Raw Directives" (functional textarea).
// The Rule Builder tab allows building 6 types of typed nginx rules with drag-reorder.
import { useState } from "react";
import {
  SettingOutlined,
  ToolOutlined,
  CodeOutlined,
  CheckOutlined,
  WarningOutlined,
  PlusOutlined,
  DownOutlined,
  DeleteOutlined,
  MenuOutlined,
  UpOutlined,
} from "@ant-design/icons";
import {
  Button,
  Modal,
  Alert,
  Tabs,
  Input,
  Typography,
  Card,
  Select,
  Switch,
  Row,
  Col,
  Dropdown,
} from "antd";
import { useInvalidate, useNotification } from "@refinedev/core";
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  useSortable,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";

import { apiClient } from "../apiClient";

// Nginx rule type definitions
export type NginxRule =
  | { type: "custom_header"; name: string; value: string; always?: boolean }
  | {
      type: "rewrite";
      pattern: string;
      replacement: string;
      flag?: "last" | "break" | "redirect" | "permanent";
    }
  | { type: "proxy_pass"; path: string; target: string }
  | {
      type: "ip_access";
      path: string;
      mode: "allow_list" | "deny_list";
      ips: string[];
    }
  | { type: "php_setting"; name: string; value: string }
  | { type: "max_upload_size"; size: string };

// Minimal shape — admin and user shells have slightly different Domain
// records but this button only cares about these fields.
export type DomainSettingsTarget = {
  id: string;
  name: string;
  user_id?: string;
  php_pool_id?: string | null;
  nginx_custom_directives?: string | null;
  nginx_rules?: NginxRule[] | null;
};

// Raw Directives editor component
const RawDirectivesEditor = ({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) => (
  <div>
    <div style={{ marginBottom: 12 }}>
      <Typography.Text strong>Raw directives</Typography.Text>
    </div>
    <Input.TextArea
      rows={14}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={`# Example:
rewrite ^/old$ /new permanent;
add_header X-Frame-Options "DENY" always;`}
      style={{
        fontFamily: "monospace",
        fontSize: 12,
      }}
    />
    <Typography.Text
      type="secondary"
      style={{ display: "block", marginTop: 8, fontSize: 12 }}
    >
      Restricted to safe directives (rewrite, add_header, proxy_pass, etc.).
      Dangerous directives are blocked.
    </Typography.Text>
  </div>
);

// Type-specific form renderers
const renderCustomHeaderBody = (
  rule: Extract<NginxRule, { type: "custom_header" }>,
  onUpdate: (field: string, value: unknown) => void
) => (
  <>
    <Row gutter={16} style={{ marginBottom: 12 }}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Header Name <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="X-Frame-Options"
          value={rule.name}
          onChange={(e) => onUpdate("name", e.target.value)}
        />
      </Col>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Value <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="DENY"
          value={rule.value}
          onChange={(e) => onUpdate("value", e.target.value)}
        />
      </Col>
    </Row>
    <Row gutter={16}>
      <Col span={24}>
        <Switch
          checked={rule.always ?? true}
          onChange={(v) => onUpdate("always", v)}
          style={{ marginRight: 8 }}
        />
        <Typography.Text>Send header on error responses</Typography.Text>
      </Col>
    </Row>
  </>
);

const renderRewriteBody = (
  rule: Extract<NginxRule, { type: "rewrite" }>,
  onUpdate: (field: string, value: unknown) => void
) => (
  <>
    <Row gutter={16} style={{ marginBottom: 12 }}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Pattern <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="^/old$"
          value={rule.pattern}
          onChange={(e) => onUpdate("pattern", e.target.value)}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12, display: "block", marginTop: 4 }}>
          Regex matched against the URI
        </Typography.Text>
      </Col>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Replacement <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="/new"
          value={rule.replacement}
          onChange={(e) => onUpdate("replacement", e.target.value)}
        />
      </Col>
    </Row>
    <Row gutter={16}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>Flag</Typography.Text>
        </div>
        <Select
          value={rule.flag ?? "last"}
          onChange={(v) => onUpdate("flag", v)}
          options={[
            { value: "last", label: "last" },
            { value: "break", label: "break" },
            { value: "redirect", label: "redirect" },
            { value: "permanent", label: "permanent" },
          ]}
          style={{ width: "100%" }}
        />
      </Col>
    </Row>
  </>
);

const renderProxyPassBody = (
  rule: Extract<NginxRule, { type: "proxy_pass" }>,
  onUpdate: (field: string, value: unknown) => void
) => (
  <>
    <Row gutter={16} style={{ marginBottom: 12 }}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Path <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="/api/"
          value={rule.path}
          onChange={(e) => onUpdate("path", e.target.value)}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12, display: "block", marginTop: 4 }}>
          Location prefix
        </Typography.Text>
      </Col>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Target URL <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="http://localhost:3000"
          value={rule.target}
          onChange={(e) => onUpdate("target", e.target.value)}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12, display: "block", marginTop: 4 }}>
          Upstream service URL
        </Typography.Text>
      </Col>
    </Row>
  </>
);

const renderIpAccessBody = (
  rule: Extract<NginxRule, { type: "ip_access" }>,
  onUpdate: (field: string, value: unknown) => void
) => (
  <>
    <Row gutter={16} style={{ marginBottom: 12 }}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Path <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="/admin/"
          value={rule.path}
          onChange={(e) => onUpdate("path", e.target.value)}
        />
      </Col>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Mode <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Select
          value={rule.mode}
          onChange={(v) => onUpdate("mode", v)}
          options={[
            { value: "allow_list", label: "Allow listed IPs" },
            { value: "deny_list", label: "Deny listed IPs" },
          ]}
          style={{ width: "100%" }}
        />
      </Col>
    </Row>
    <Row gutter={16}>
      <Col span={24}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            IP Addresses <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Select
          mode="tags"
          placeholder="192.168.1.1, 10.0.0.0/8"
          value={rule.ips}
          onChange={(v) => onUpdate("ips", v)}
          tokenSeparators={[",", " ", "\n"]}
          style={{ width: "100%" }}
        />
      </Col>
    </Row>
  </>
);

const renderPhpSettingBody = (
  rule: Extract<NginxRule, { type: "php_setting" }>,
  onUpdate: (field: string, value: unknown) => void
) => (
  <>
    <Row gutter={16}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            PHP Directive <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="memory_limit"
          value={rule.name}
          onChange={(e) => onUpdate("name", e.target.value)}
        />
      </Col>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Value <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="512M"
          value={rule.value}
          onChange={(e) => onUpdate("value", e.target.value)}
        />
      </Col>
    </Row>
  </>
);

const renderMaxUploadSizeBody = (
  rule: Extract<NginxRule, { type: "max_upload_size" }>,
  onUpdate: (field: string, value: unknown) => void
) => (
  <>
    <Row gutter={16}>
      <Col span={12}>
        <div style={{ marginBottom: 8 }}>
          <Typography.Text>
            Size <span style={{ color: "#ff4d4f" }}>*</span>
          </Typography.Text>
        </div>
        <Input
          placeholder="100M"
          value={rule.size}
          onChange={(e) => onUpdate("size", e.target.value)}
        />
        <Typography.Text type="secondary" style={{ fontSize: 12, display: "block", marginTop: 4 }}>
          e.g. 10M, 1G
        </Typography.Text>
      </Col>
    </Row>
  </>
);

// Sortable rule card
interface SortableRuleCardProps {
  idx: number;
  rule: NginxRule;
  isExpanded: boolean;
  onToggleExpanded: (idx: number) => void;
  onRemove: (idx: number) => void;
  onUpdate: (idx: number, field: string, value: unknown) => void;
}

const SortableRuleCard = ({
  idx,
  rule,
  isExpanded,
  onToggleExpanded,
  onRemove,
  onUpdate,
}: SortableRuleCardProps) => {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: idx,
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  };

  const getRuleTypeLabel = (type: NginxRule["type"]) => {
    const labels: Record<NginxRule["type"], string> = {
      custom_header: "Custom Header",
      rewrite: "Rewrite",
      proxy_pass: "Proxy Pass",
      ip_access: "IP Access",
      php_setting: "PHP Setting",
      max_upload_size: "Max Upload Size",
    };
    return labels[type];
  };

  const getRuleSummary = (rule: NginxRule): string => {
    switch (rule.type) {
      case "custom_header":
        return `${rule.name}: ${rule.value}`;
      case "rewrite":
        return `${rule.pattern} → ${rule.replacement}`;
      case "proxy_pass":
        return `${rule.path} → ${rule.target}`;
      case "ip_access":
        return `${rule.path} (${rule.mode})`;
      case "php_setting":
        return `${rule.name} = ${rule.value}`;
      case "max_upload_size":
        return `Max: ${rule.size}`;
    }
  };

  return (
    <div ref={setNodeRef} style={style}>
      <Card size="small" style={{ marginBottom: 12 }} bodyStyle={{ padding: 12 }}>
        <div style={{ display: "flex", alignItems: "center", marginBottom: isExpanded ? 12 : 0, gap: 8 }}>
          <button
            {...attributes}
            {...listeners}
            style={{
              cursor: "grab",
              background: "none",
              border: "none",
              padding: 4,
              display: "flex",
              alignItems: "center",
            }}
          >
            <MenuOutlined style={{ color: "#999" }} />
          </button>

          <Typography.Text strong style={{ fontSize: 13 }}>
            {getRuleTypeLabel(rule.type)}
          </Typography.Text>

          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {getRuleSummary(rule)}
          </Typography.Text>

          <div style={{ flex: 1 }} />

          <Button
            type="text"
            icon={isExpanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => onToggleExpanded(idx)}
            style={{ padding: 4 }}
          />
          <Button
            danger
            icon={<DeleteOutlined />}
            type="text"
            size="small"
            onClick={() => onRemove(idx)}
          />
        </div>

        {isExpanded && (
          <div style={{ paddingTop: 8 }}>
            {rule.type === "custom_header" &&
              renderCustomHeaderBody(
                rule as Extract<NginxRule, { type: "custom_header" }>,
                (field, value) => onUpdate(idx, field, value)
              )}
            {rule.type === "rewrite" &&
              renderRewriteBody(
                rule as Extract<NginxRule, { type: "rewrite" }>,
                (field, value) => onUpdate(idx, field, value)
              )}
            {rule.type === "proxy_pass" &&
              renderProxyPassBody(
                rule as Extract<NginxRule, { type: "proxy_pass" }>,
                (field, value) => onUpdate(idx, field, value)
              )}
            {rule.type === "ip_access" &&
              renderIpAccessBody(
                rule as Extract<NginxRule, { type: "ip_access" }>,
                (field, value) => onUpdate(idx, field, value)
              )}
            {rule.type === "php_setting" &&
              renderPhpSettingBody(
                rule as Extract<NginxRule, { type: "php_setting" }>,
                (field, value) => onUpdate(idx, field, value)
              )}
            {rule.type === "max_upload_size" &&
              renderMaxUploadSizeBody(
                rule as Extract<NginxRule, { type: "max_upload_size" }>,
                (field, value) => onUpdate(idx, field, value)
              )}
          </div>
        )}
      </Card>
    </div>
  );
};

// Rule Builder component
const RuleBuilder = ({
  rules,
  onRulesChange,
}: {
  rules: NginxRule[];
  onRulesChange: (rules: NginxRule[]) => void;
}) => {
  const [expandedCards, setExpandedCards] = useState<Set<number>>(new Set());

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;

    if (over && active.id !== over.id) {
      const oldIndex = Number(active.id);
      const newIndex = Number(over.id);
      onRulesChange(arrayMove(rules, oldIndex, newIndex));
    }
  };

  const addRule = (type: NginxRule["type"]) => {
    let newRule: NginxRule;

    switch (type) {
      case "custom_header":
        newRule = { type: "custom_header", name: "", value: "", always: true };
        break;
      case "rewrite":
        newRule = { type: "rewrite", pattern: "", replacement: "", flag: "last" };
        break;
      case "proxy_pass":
        newRule = { type: "proxy_pass", path: "/", target: "" };
        break;
      case "ip_access":
        newRule = { type: "ip_access", path: "/", mode: "allow_list", ips: [] };
        break;
      case "php_setting":
        newRule = { type: "php_setting", name: "", value: "" };
        break;
      case "max_upload_size":
        newRule = { type: "max_upload_size", size: "" };
        break;
    }

    const newRules = [...rules, newRule];
    onRulesChange(newRules);
    // Auto-expand the new card
    setExpandedCards(new Set(expandedCards).add(rules.length));
  };

  const removeRule = (idx: number) => {
    onRulesChange(rules.filter((_, i) => i !== idx));
  };

  const updateRule = (idx: number, field: string, value: unknown) => {
    const updated = [...rules];
    updated[idx] = { ...updated[idx], [field]: value };
    onRulesChange(updated);
  };

  const addMenuItems = [
    { key: "custom_header", label: "Custom Header", icon: <PlusOutlined /> },
    { key: "rewrite", label: "Rewrite", icon: <PlusOutlined /> },
    { key: "proxy_pass", label: "Proxy Pass", icon: <PlusOutlined /> },
    { key: "ip_access", label: "IP Access", icon: <PlusOutlined /> },
    { key: "php_setting", label: "PHP Setting", icon: <PlusOutlined /> },
    { key: "max_upload_size", label: "Max Upload Size", icon: <PlusOutlined /> },
  ];

  return (
    <div>
      <Typography.Paragraph style={{ color: "#666", marginBottom: 16 }}>
        Add rules using the form below. They will be converted to nginx directives automatically.
      </Typography.Paragraph>

      {rules.length === 0 ? (
        <div
          style={{
            padding: "32px 24px",
            textAlign: "center",
            color: "#999",
          }}
        >
          <Typography.Text type="secondary">
            No rules yet. Click Add Rule to get started.
          </Typography.Text>
        </div>
      ) : (
        <div style={{ marginBottom: 16 }}>
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            onDragEnd={handleDragEnd}
          >
            <SortableContext
              items={rules.map((_, idx) => idx)}
              strategy={verticalListSortingStrategy}
            >
              {rules.map((rule, idx) => (
                <SortableRuleCard
                  key={idx}
                  idx={idx}
                  rule={rule}
                  isExpanded={expandedCards.has(idx)}
                  onToggleExpanded={(i) => {
                    const newSet = new Set(expandedCards);
                    if (newSet.has(i)) {
                      newSet.delete(i);
                    } else {
                      newSet.add(i);
                    }
                    setExpandedCards(newSet);
                  }}
                  onRemove={removeRule}
                  onUpdate={updateRule}
                />
              ))}
            </SortableContext>
          </DndContext>
        </div>
      )}

      <div style={{ textAlign: "center", marginBottom: 16 }}>
        <Dropdown
          menu={{
            items: addMenuItems.map((item) => ({
              ...item,
              onClick: () => addRule(item.key as NginxRule["type"]),
            })),
          }}
        >
          <Button icon={<PlusOutlined />}>
            Add Rule <DownOutlined />
          </Button>
        </Dropdown>
      </div>
    </div>
  );
};

export const DomainSettingsButton = ({
  domain,
}: {
  domain: DomainSettingsTarget;
}) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const handleCloseModal = () => setIsModalOpen(false);
  const [directivesValue, setDirectivesValue] = useState(
    domain.nginx_custom_directives ?? ""
  );
  const [rules, setRules] = useState<NginxRule[]>(domain.nginx_rules ?? []);
  const [isSaving, setIsSaving] = useState(false);
  const invalidate = useInvalidate();
  const { open } = useNotification();

  const handleOpenModal = async () => {
    // Re-sync from prop in case the values were updated elsewhere
    setDirectivesValue(domain.nginx_custom_directives ?? "");
    setRules(domain.nginx_rules ?? []);
    setIsModalOpen(true);
  };


  const handleApply = async () => {
    setIsSaving(true);
    try {
      await apiClient.patch(`/domains/${domain.id}`, {
        nginx_custom_directives: directivesValue,
        nginx_rules: rules,
      });
      open?.({
        type: "success",
        message: "Nginx config saved",
      });
      invalidate({ resource: "domains", invalidates: ["list"] });
      setIsModalOpen(false);
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      open?.({
        type: "error",
        message: "Failed to save",
        description: e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <>
      <Button
        type="text"
        size="small"
        icon={<SettingOutlined />}
        onClick={handleOpenModal}
      >
        Nginx Directives
      </Button>

      <Modal
        title={`Nginx Directives for ${domain.name}`}
        open={isModalOpen}
        onCancel={handleCloseModal}
        width={720}
        footer={[
          <Button key="cancel" onClick={handleCloseModal}>
            Cancel
          </Button>,
          <Button
            key="apply"
            type="primary"
            icon={<CheckOutlined />}
            onClick={handleApply}
            loading={isSaving}
          >
            Apply
          </Button>,
        ]}
      >
        <Alert
          type="warning"
          icon={<WarningOutlined />}
          message="Use with caution"
          description="Incorrect directives can break your website. Changes are tested with nginx before applying, but you are responsible for ensuring your configuration is correct."
          showIcon
          style={{ marginBottom: 24 }}
        />

        <Tabs
          defaultActiveKey="raw"
          items={[
            {
              key: "builder",
              label: (
                <span>
                  <ToolOutlined /> Rule Builder
                </span>
              ),
              children: <RuleBuilder rules={rules} onRulesChange={setRules} />,
            },
            {
              key: "raw",
              label: (
                <span>
                  <CodeOutlined /> Raw Directives
                </span>
              ),
              children: (
                <RawDirectivesEditor
                  value={directivesValue}
                  onChange={setDirectivesValue}
                />
              ),
            },
          ]}
        />
      </Modal>
    </>
  );
};
