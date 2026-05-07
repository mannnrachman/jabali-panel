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
  initSettingsFlow,
  lookupSecretReveal,
  renderableFields,
  submitSettingsFlow,
  totpEnrolmentDisplay,
  type KratosFlow,
  type RenderableField,
} from "../../kratos";
import { MyProfileBackupCard } from "./MyProfileBackupCard";
import { MyProfileUsageCard } from "./MyProfileUsageCard";

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

  // Auto-start the Kratos settings flow when the page loads without a
  // ?flow= param. Use the JSON API instead of the browser flow's 303
  // dance so we can handle privileged-session refresh inline without
  // the multi-stage window.location.assign chain that kept landing
  // admins on /dashboard. initSettingsFlow returns one of three
  // states: flow ready (render), refresh required (kick a Kratos
  // login flow with refresh=true), or unauthenticated (back to login).
  useEffect(() => {
    if (flowID) return;
    let cancelled = false;
    setFlowLoading(true);
    initSettingsFlow().then((res) => {
      if (cancelled) return;
      switch (res.kind) {
        case "flow":
          // Push the flow id into the URL so refresh / back-button
          // behaviour matches the Kratos-redirect path.
          navigate(`${location.pathname}?flow=${res.flow.id}`, { replace: true });
          break;
        case "refresh_required": {
          sessionStorage.setItem("post_login_return_to", location.pathname);
          const ret = encodeURIComponent(location.pathname);
          window.location.assign(
            `/.ory/self-service/login/browser?refresh=true&return_to=${ret}`,
          );
          break;
        }
        case "unauthenticated":
          window.location.assign("/login");
          break;
        case "error":
          setFlowError(res.message);
          setFlowLoading(false);
          break;
      }
    });
    return () => {
      cancelled = true;
    };
  }, [flowID, location.pathname, navigate]);

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
    // Strip ?flow=<id> while keeping the rest of the URL — the same
    // route mounts under both shells (/jabali-admin/profile +
    // /jabali-panel/profile) so use the current pathname rather than
    // hard-coding the user-shell path.
    navigate(location.pathname, { replace: true });
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
            // The first useEffect above kicked window.location to the
            // Kratos browser flow — show a spinner during the round-trip.
            <div style={{ textAlign: "center", padding: 24 }}>
              <Spin />
            </div>
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
  const totpDisplay = group === "totp" ? totpEnrolmentDisplay(flow) : null;
  const recoveryCodes = group === "lookup_secret" ? lookupSecretReveal(flow) : null;

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
      {totpDisplay && (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message="Scan this QR code with your authenticator app"
          description={
            <div>
              {totpDisplay.qrSrc && (
                <img
                  src={totpDisplay.qrSrc}
                  alt="TOTP QR code"
                  style={{ display: "block", marginTop: 8, width: 220, height: 220 }}
                />
              )}
              {totpDisplay.secret && (
                <Typography.Paragraph copyable style={{ marginTop: 8, marginBottom: 0 }}>
                  <Typography.Text code>{totpDisplay.secret}</Typography.Text>
                </Typography.Paragraph>
              )}
            </div>
          }
        />
      )}
      {recoveryCodes && recoveryCodes.length > 0 && (
        <Alert
          type="success"
          showIcon
          style={{ marginBottom: 16 }}
          message="Save these recovery codes — shown once"
          description={
            <Typography.Paragraph
              copyable={{ text: recoveryCodes.join("\n") }}
              style={{ margin: 0, fontFamily: "monospace", whiteSpace: "pre" }}
            >
              {recoveryCodes.join("\n")}
            </Typography.Paragraph>
          }
        />
      )}
      <Form
        layout="vertical"
        requiredMark={false}
        onFinish={submit}
        initialValues={Object.fromEntries(fields.map((f) => [f.name, f.value]))}
      >
        {fields.map((f) => renderField(f))}
        {/* Submit-type Kratos nodes carry the action keyword (e.g.
            totp_unlink=true, lookup_secret_regenerate=true). Render
            one button per submit field; the button's onClick submits
            the form with that action key set. When no submit field
            exists, fall back to the generic group-default button
            (e.g. password change has only inputs, no explicit
            method-action node). */}
        {fields.filter((f) => f.kind === "submit").length > 0 ? (
          <Space wrap>
            {fields
              .filter((f) => f.kind === "submit")
              .map((f) => (
                <Button
                  key={f.name + "=" + f.value}
                  type="primary"
                  loading={submitting}
                  danger={f.name.endsWith("_unlink")}
                  onClick={() =>
                    submit({ ...{}, [f.name]: f.value })
                  }
                >
                  {submitButtonLabel(group, f.name)}
                </Button>
              ))}
          </Space>
        ) : (
          <Form.Item style={{ marginBottom: 0 }}>
            <Button type="primary" htmlType="submit" loading={submitting}>
              {submitLabel(group)}
            </Button>
          </Form.Item>
        )}
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

// Per-action button label. Kratos returns submit-type input nodes for
// the actions that need a dedicated button (totp_unlink, lookup_secret_
// regenerate, lookup_secret_disable, webauthn_remove). Localise to
// English here so the UI doesn't leak Kratos's internal field names.
function submitButtonLabel(group: string, name: string): string {
  switch (name) {
    case "totp_unlink":
      return "Disable two-factor";
    case "lookup_secret_regenerate":
      return "Regenerate backup codes";
    case "lookup_secret_disable":
      return "Disable backup codes";
    case "webauthn_remove":
      return "Remove security key";
    case "totp_code":
    case "method":
      return submitLabel(group);
    default:
      return humanizeName(name);
  }
}
