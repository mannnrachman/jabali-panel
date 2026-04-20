// Login page — driven by Ory Kratos browser self-service flow.
//
// On mount we call initLoginFlow() to get a fresh flow object (CSRF token
// + ui.nodes describing what inputs Kratos wants). We render those nodes
// generically, submit to flow.ui.action with the user-entered values, and
// either:
//   - {session} came back → login finished, navigate by role
//   - {flow} came back    → AAL2 step required (e.g. TOTP). Re-render.
//   - error               → surface the message, let user retry.
//
// Kratos does AAL2 by returning an updated flow whose nodes belong to a
// new group (usually "totp"). The renderer reads node.group and groups
// the form visually — each non-default group becomes its own submit
// button, matching the M5c behaviour the legacy panel had.
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Space,
  Spin,
  Typography,
  theme,
} from "antd";

import { homeForRole } from "../authProvider";
import { clearIdentity, getIdentity } from "../identity";
import {
  csrfToken,
  flowMessages,
  initLoginFlow,
  renderableFields,
  submitLoginFlow,
  type KratosFlow,
  type RenderableField,
} from "../kratos";

export const LoginPage = () => {
  const navigate = useNavigate();
  const { token } = theme.useToken();

  const [flow, setFlow] = useState<KratosFlow | null>(null);
  const [loadingFlow, setLoadingFlow] = useState(true);
  const [initError, setInitError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    initLoginFlow()
      .then((f) => {
        if (cancelled) return;
        setFlow(f);
      })
      .catch(() => {
        if (cancelled) return;
        setInitError(
          "Could not start the sign-in flow. Check that the identity service is running and refresh the page.",
        );
      })
      .finally(() => {
        if (!cancelled) setLoadingFlow(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const onFinish = async (group: string, values: Record<string, unknown>) => {
    if (!flow) return;
    const body: Record<string, string> = {
      csrf_token: csrfToken(flow),
      method: group,
    };
    for (const [k, v] of Object.entries(values)) {
      body[k] = v == null ? "" : String(v);
    }
    const result = await submitLoginFlow(flow, body);
    if (result.kind === "success") {
      clearIdentity();
      const me = await getIdentity();
      navigate(homeForRole(me?.isAdmin ?? false), { replace: true });
      return;
    }
    if (result.kind === "continue") {
      setFlow(result.flow);
      return;
    }
    // Error — surface into the flow messages via a local synthetic flow so
    // the renderer path handles it uniformly.
    setFlow({
      ...flow,
      ui: {
        ...flow.ui,
        messages: [
          ...(flow.ui.messages ?? []),
          { id: 0, text: result.message, type: "error" },
        ],
      },
    });
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: token.colorBgLayout,
      }}
    >
      <Card style={{ width: 420 }}>
        <Space orientation="vertical" size="large" style={{ width: "100%" }}>
          <Typography.Title level={3} style={{ margin: 0 }}>
            Jabali Panel
          </Typography.Title>

          {initError && (
            <Alert title={initError} type="error" showIcon />
          )}

          {loadingFlow && !initError && (
            <div style={{ textAlign: "center", padding: 24 }}>
              <Spin />
            </div>
          )}

          {flow && <FlowForm flow={flow} onFinish={onFinish} />}
        </Space>
      </Card>
    </div>
  );
};

type FlowFormProps = {
  flow: KratosFlow;
  onFinish: (group: string, values: Record<string, unknown>) => Promise<void>;
};

/**
 * Renders each credential group in the flow as its own AntD form section.
 * Kratos puts CSRF + method-selector nodes in "default"; the other groups
 * (usually just one at a time — "password" on AAL1, "totp" on AAL2) each
 * carry their own submit button. We pick the first non-default group to
 * render as the active section; the "default" nodes are included as
 * hidden inputs on submission.
 */
function FlowForm({ flow, onFinish }: FlowFormProps) {
  const topErrors = flowMessages(flow);
  const activeGroup = pickActiveGroup(flow);

  if (!activeGroup) {
    return (
      <Alert
        title="No credential method is currently configured for your account. Contact an administrator."
        type="warning"
        showIcon
      />
    );
  }

  const fields = renderableFields(flow, activeGroup);
  return (
    <>
      {topErrors.map((msg, i) => (
        <Alert key={i} title={msg} type="error" showIcon />
      ))}
      <GroupForm
        groupName={activeGroup}
        fields={fields}
        onSubmit={(values) => onFinish(activeGroup, values)}
      />
    </>
  );
}

function pickActiveGroup(flow: KratosFlow): string | null {
  // Priority order: prefer the first non-default group that isn't one of
  // the "link existing session" helpers. In practice Kratos surfaces one
  // credential method per AAL step, so this loop returns either "password"
  // on AAL1 or the 2FA method on AAL2.
  const seen = new Set<string>();
  for (const node of flow.ui.nodes) {
    if (node.group === "default") continue;
    if (!seen.has(node.group)) seen.add(node.group);
  }
  // Preferred order if multiple groups show up at once.
  const preferred = ["totp", "lookup_secret", "password", "webauthn"];
  for (const g of preferred) {
    if (seen.has(g)) return g;
  }
  return seen.values().next().value ?? null;
}

type GroupFormProps = {
  groupName: string;
  fields: RenderableField[];
  onSubmit: (values: Record<string, unknown>) => Promise<void>;
};

function GroupForm({ groupName, fields, onSubmit }: GroupFormProps) {
  const [submitting, setSubmitting] = useState(false);

  const submit = async (values: Record<string, unknown>) => {
    setSubmitting(true);
    try {
      await onSubmit(values);
    } finally {
      setSubmitting(false);
    }
  };

  const title = groupTitle(groupName);

  return (
    <Form
      layout="vertical"
      requiredMark={false}
      onFinish={submit}
      initialValues={Object.fromEntries(fields.map((f) => [f.name, f.value]))}
    >
      {title && (
        <Typography.Paragraph style={{ marginBottom: 8 }}>
          {title}
        </Typography.Paragraph>
      )}
      {fields.map((f) => renderField(f))}
      <Form.Item style={{ marginBottom: 0 }}>
        <Button type="primary" htmlType="submit" block loading={submitting}>
          {submitLabel(groupName)}
        </Button>
      </Form.Item>
    </Form>
  );
}

function renderField(f: RenderableField) {
  if (f.kind === "hidden") {
    // Hidden inputs (csrf_token, method) are tracked via the Form's
    // initialValues (set on GroupForm). We still declare the Form.Item so
    // the field is part of the form's value snapshot on submit; no inner
    // initialValue here (Form-level initialValues is the single owner).
    return (
      <Form.Item key={f.name} name={f.name} noStyle hidden>
        <Input type="hidden" />
      </Form.Item>
    );
  }
  if (f.kind === "submit") {
    // Kratos embeds submit buttons in the node tree; we render the primary
    // submit ourselves (see GroupForm), so skip to avoid duplicates.
    return null;
  }

  const Control =
    f.kind === "password" ? (
      <Input.Password autoComplete={f.autocomplete ?? "current-password"} />
    ) : (
      <Input
        type={f.kind}
        inputMode={f.kind === "number" || f.kind === "tel" ? "numeric" : undefined}
        autoComplete={f.autocomplete}
        autoFocus={f.kind === "email" || f.kind === "text"}
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
  // "identifier" → "Identifier", "totp_code" → "Totp code", etc. Kratos
  // usually supplies a label in node.meta.label.text so this is a fallback
  // for nodes that don't.
  const spaced = name.replace(/_/g, " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

function groupTitle(group: string): string | null {
  switch (group) {
    case "totp":
      return "Enter the 6-digit code from your authenticator app.";
    case "lookup_secret":
      return "Enter one of your backup codes.";
    case "password":
      return null; // Email + Password labels are self-explanatory.
    case "webauthn":
      return "Use your security key to sign in.";
    default:
      return null;
  }
}

function submitLabel(group: string): string {
  switch (group) {
    case "totp":
    case "lookup_secret":
      return "Verify";
    default:
      return "Sign in";
  }
}
