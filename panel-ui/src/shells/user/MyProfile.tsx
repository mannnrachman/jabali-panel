// MyProfile — user-panel profile page.
//
// Post-M20 auth lives in Kratos. Password changes and TOTP enrolment
// happen via Kratos's self-service settings flow. The "Manage account
// security" button kicks the browser through /.ory/self-service/settings/browser;
// Kratos then redirects to its configured ui_url (`/settings`), which
// the SPA bounces to `/jabali-panel/profile?flow=<id>`. When this page
// sees `?flow=<id>` it fetches the flow and renders the Kratos node
// tree inline inside the Security card — no extra page, no extra tab.
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  Space,
  Spin,
  Typography,
  message,
} from "antd";
import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router";

import { getIdentity, type Identity } from "../../identity";
import {
  csrfToken,
  flowMessages,
  getSettingsFlow,
  renderableFields,
  submitSettingsFlow,
  type KratosFlow,
  type RenderableField,
} from "../../kratos";
import { MyProfileBackupCard } from "./MyProfileBackupCard";
import { MyProfileUsageCard } from "./MyProfileUsageCard";

const SETTINGS_BROWSER_URL = "/.ory/self-service/settings/browser";

export function MyProfile() {
  const navigate = useNavigate();
  const location = useLocation();
  const [me, setMe] = useState<Identity | null>(null);

  const flowID = useMemo(() => {
    return new URLSearchParams(location.search).get("flow");
  }, [location.search]);

  const [flow, setFlow] = useState<KratosFlow | null>(null);
  const [flowLoading, setFlowLoading] = useState(false);
  const [flowError, setFlowError] = useState<string | null>(null);

  useEffect(() => {
    getIdentity().then(setMe);
  }, []);

  useEffect(() => {
    if (!flowID) {
      setFlow(null);
      setFlowError(null);
      return;
    }
    let cancelled = false;
    setFlowLoading(true);
    setFlowError(null);
    getSettingsFlow(flowID)
      .then((f) => {
        if (cancelled) return;
        setFlow(f);
      })
      .catch(() => {
        if (cancelled) return;
        setFlowError(
          "Could not load the security update form. The link may have expired — try Manage account security again.",
        );
      })
      .finally(() => {
        if (!cancelled) setFlowLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [flowID]);

  const onSubmit = async (group: string, values: Record<string, unknown>) => {
    if (!flow) return;
    const body: Record<string, string> = {
      csrf_token: csrfToken(flow),
      method: group,
    };
    for (const [k, v] of Object.entries(values)) {
      body[k] = v == null ? "" : String(v);
    }
    const result = await submitSettingsFlow(flow, body);
    if (result.kind === "continue") {
      setFlow(result.flow);
      const successMsg = (result.flow.ui.messages ?? []).find((m) => m.type === "success");
      if (successMsg) {
        message.success(successMsg.text);
      }
      return;
    }
    if (result.kind === "error") {
      message.error(result.message);
      return;
    }
  };

  const closeFlow = () => {
    navigate("/jabali-panel/profile", { replace: true });
  };

  return (
    <div style={{ maxWidth: 720, margin: "0 auto" }}>
      <Space orientation="vertical" size="large" style={{ width: "100%" }}>
        <Typography.Title level={2} style={{ margin: 0 }}>
          My profile
        </Typography.Title>

        <Card title="Account" loading={!me}>
          {me && (
            <Descriptions column={1}>
              <Descriptions.Item label="Email">{me.email}</Descriptions.Item>
              <Descriptions.Item label="User ID">
                <Typography.Text code>{me.id}</Typography.Text>
              </Descriptions.Item>
            </Descriptions>
          )}
        </Card>

        <Card
          title="Security"
          extra={
            flow && (
              <Button type="text" onClick={closeFlow}>
                Done
              </Button>
            )
          }
        >
          {!flowID && (
            <>
              <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>
                Password changes and two-factor authentication are managed by
                our identity provider.
              </Typography.Paragraph>
              <Button type="primary" href={SETTINGS_BROWSER_URL}>
                Manage account security
              </Button>
            </>
          )}

          {flowID && flowLoading && (
            <div style={{ textAlign: "center", padding: 24 }}>
              <Spin />
            </div>
          )}

          {flowID && flowError && (
            <Alert
              type="error"
              showIcon
              message={flowError}
              action={
                <Button size="small" type="link" onClick={closeFlow}>
                  Reset
                </Button>
              }
            />
          )}

          {flow && !flowError && (
            <SettingsFlowForms flow={flow} onSubmit={onSubmit} />
          )}
        </Card>

        {me && <MyProfileUsageCard userId={me.id} />}
        {me && <MyProfileBackupCard />}
      </Space>
    </div>
  );
}

type SettingsFormsProps = {
  flow: KratosFlow;
  onSubmit: (group: string, values: Record<string, unknown>) => Promise<void>;
};

// Settings flows surface multiple credential groups at once (password +
// totp + lookup_secret). Render each non-default group as its own
// section so password change + 2FA enrolment live side by side.
function SettingsFlowForms({ flow, onSubmit }: SettingsFormsProps) {
  const topErrors = flowMessages(flow);
  const groups = collectGroups(flow);

  if (groups.length === 0) {
    return (
      <Alert
        type="warning"
        showIcon
        message="No editable security settings are exposed for your account."
      />
    );
  }

  return (
    <Space orientation="vertical" size="middle" style={{ width: "100%" }}>
      {topErrors.map((msg, i) => (
        <Alert key={i} type="error" showIcon message={msg} />
      ))}
      {groups.map((g) => (
        <SettingsGroupForm
          key={g}
          flow={flow}
          group={g}
          onSubmit={(v) => onSubmit(g, v)}
        />
      ))}
    </Space>
  );
}

function collectGroups(flow: KratosFlow): string[] {
  const seen = new Set<string>();
  const order: string[] = [];
  for (const node of flow.ui.nodes) {
    if (node.group === "default" || node.group === "profile") continue;
    if (seen.has(node.group)) continue;
    seen.add(node.group);
    order.push(node.group);
  }
  // Stable ordering: password first, then 2FA methods.
  const preferred = ["password", "totp", "lookup_secret", "webauthn"];
  return [...preferred.filter((p) => seen.has(p)), ...order.filter((g) => !preferred.includes(g))];
}

type GroupFormProps = {
  flow: KratosFlow;
  group: string;
  onSubmit: (values: Record<string, unknown>) => Promise<void>;
};

function SettingsGroupForm({ flow, group, onSubmit }: GroupFormProps) {
  const [submitting, setSubmitting] = useState(false);
  const fields = renderableFields(flow, group);

  const submit = async (values: Record<string, unknown>) => {
    setSubmitting(true);
    try {
      await onSubmit(values);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card type="inner" title={groupTitle(group)} size="small">
      <Form
        layout="vertical"
        requiredMark={false}
        onFinish={submit}
        initialValues={Object.fromEntries(fields.map((f) => [f.name, f.value]))}
      >
        {fields.map((f) => renderField(f))}
        <Form.Item style={{ marginBottom: 0 }}>
          <Button type="primary" htmlType="submit" loading={submitting}>
            {submitLabel(group)}
          </Button>
        </Form.Item>
      </Form>
    </Card>
  );
}

function renderField(f: RenderableField) {
  if (f.kind === "hidden") {
    return (
      <Form.Item key={f.name} name={f.name} noStyle hidden>
        <Input type="hidden" />
      </Form.Item>
    );
  }
  if (f.kind === "submit") {
    return null;
  }
  const Control =
    f.kind === "password" ? (
      <Input.Password autoComplete={f.autocomplete ?? "new-password"} />
    ) : (
      <Input
        type={f.kind}
        inputMode={f.kind === "number" || f.kind === "tel" ? "numeric" : undefined}
        autoComplete={f.autocomplete}
      />
    );

  return (
    <Form.Item
      key={f.name}
      name={f.name}
      label={f.label ?? humanizeName(f.name)}
      rules={[{ required: f.required, message: "Required" }]}
      help={f.errors.length ? f.errors.join("; ") : undefined}
      validateStatus={f.errors.length ? "error" : undefined}
    >
      {Control}
    </Form.Item>
  );
}

function humanizeName(name: string): string {
  const spaced = name.replace(/_/g, " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

function groupTitle(group: string): string {
  switch (group) {
    case "password":
      return "Change password";
    case "totp":
      return "Two-factor authentication (TOTP)";
    case "lookup_secret":
      return "Backup codes";
    case "webauthn":
      return "Security keys";
    default:
      return group;
  }
}

function submitLabel(group: string): string {
  switch (group) {
    case "password":
      return "Update password";
    case "totp":
      return "Save TOTP";
    case "lookup_secret":
      return "Generate backup codes";
    case "webauthn":
      return "Save security key";
    default:
      return "Save";
  }
}
