// OAuth 2 consent page.
//
// Hydra's login-consent flow redirects the browser here when an OIDC
// client (installed WordPress, future Drupal/Joomla/etc.) requests
// consent for a non-trusted client. Trusted panel-managed installs
// auto-accept server-side and never hit this route (see
// panel-api/internal/api/oauth2_flow.go's consent-start handler).
//
// Shape of the interaction:
//   1. Mount: read `?challenge=...` from the URL, GET
//      /api/v1/oauth2/consent/:challenge to fetch metadata (client
//      name, requested scopes with labels, subject).
//   2. Render: AntD Card with the client name as the title, each
//      requested scope as a card row with the scope's Short noun
//      phrase bold + Long description beneath.
//   3. Submit: Allow → POST /oauth2-consent/accept with the full
//      requested scope set as grant_scope; Deny → POST
//      /oauth2-consent/deny. Either way the backend returns
//      { redirect_to } and we navigate there via window.location —
//      Hydra owns the post-consent redirect shape.
//
// Scope labels come from hydraclient.ScopeLabels on the backend (via
// /api/v1/oauth2/consent/:challenge), NOT from a frontend map. This
// is deliberate: a UI-side hardcoded label would drift from the
// backend's catalog and users could end up approving scopes the
// panel hasn't reviewed.
//
// CSRF: the consent_challenge itself is Hydra-signed + one-shot
// (see oauth2_flow.go's handler docstring). The Kratos session
// cookie is attached on same-origin fetches automatically. Together
// those close the CSRF gap; no separate token needed.

import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import {
  Alert,
  Button,
  Card,
  Space,
  Spin,
  Typography,
  theme,
} from "antd";
import {
  CheckOutlined,
  CloseOutlined,
  LockOutlined,
} from "@ant-design/icons";

const { Title, Paragraph, Text } = Typography;

interface ScopeWithLabel {
  scope: string;
  short: string;
  long: string;
}

interface ConsentMetadata {
  client_name: string;
  requested_scope: ScopeWithLabel[];
  subject: string;
}

// postConsent hits the backend's /oauth2-consent/{accept,deny}
// endpoints. These live OUTSIDE /api/v1 (Hydra's login-consent flow
// doesn't belong to the app's REST surface), so we use fetch directly
// rather than the apiClient axios instance.
async function postConsent(
  action: "accept" | "deny",
  body: object,
): Promise<{ redirect_to: string }> {
  const resp = await fetch(`/oauth2-consent/${action}`, {
    method: "POST",
    credentials: "include", // send the Kratos session cookie
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!resp.ok) {
    const bodyText = await resp.text().catch(() => "");
    throw new Error(
      `Consent ${action} failed (HTTP ${resp.status}): ${bodyText}`,
    );
  }
  return (await resp.json()) as { redirect_to: string };
}

export function ConsentPage() {
  const { token } = theme.useToken();
  const [params] = useSearchParams();
  const challenge = params.get("challenge") ?? "";

  const [metadata, setMetadata] = useState<ConsentMetadata | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState<"accept" | "deny" | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Mount-time fetch. Abort on unmount so a slow fetch doesn't
  // setState on a dead component.
  useEffect(() => {
    if (!challenge) {
      setLoadError(
        "This page needs a consent challenge to know which app is asking. Return to the app and start the sign-in flow again.",
      );
      setLoading(false);
      return;
    }
    const abort = new AbortController();
    fetch(`/api/v1/oauth2/consent/${encodeURIComponent(challenge)}`, {
      credentials: "include",
      signal: abort.signal,
    })
      .then(async (resp) => {
        if (!resp.ok) {
          // 404 = challenge unknown/expired. Give the user an
          // actionable message rather than a raw status code.
          if (resp.status === 404) {
            throw new Error(
              "This consent request is no longer valid. It may have expired or been used already. Start again from the app you were trying to sign in to.",
            );
          }
          if (resp.status === 401) {
            throw new Error(
              "Your session expired. Sign in again and retry from the app.",
            );
          }
          throw new Error(`Could not load consent request (HTTP ${resp.status}).`);
        }
        return (await resp.json()) as ConsentMetadata;
      })
      .then((data) => setMetadata(data))
      .catch((err) => {
        if (err.name === "AbortError") return;
        setLoadError(err.message || "Failed to load consent request.");
      })
      .finally(() => setLoading(false));
    return () => abort.abort();
  }, [challenge]);

  const handleAccept = async () => {
    if (!metadata) return;
    setSubmitting("accept");
    setSubmitError(null);
    try {
      // Grant the full requested scope set. The user approving the
      // card means approving everything on it — per-scope
      // granularity isn't in M16 (plan §2 deferred features). If we
      // ever add per-scope checkboxes, this is where the filter
      // would plug in.
      const grantScope = metadata.requested_scope.map((s) => s.scope);
      const { redirect_to } = await postConsent("accept", {
        challenge,
        grant_scope: grantScope,
      });
      window.location.href = redirect_to;
    } catch (err) {
      setSubmitError((err as Error).message);
      setSubmitting(null);
    }
  };

  const handleDeny = async () => {
    setSubmitting("deny");
    setSubmitError(null);
    try {
      const { redirect_to } = await postConsent("deny", { challenge });
      window.location.href = redirect_to;
    } catch (err) {
      setSubmitError((err as Error).message);
      setSubmitting(null);
    }
  };

  // Layout: full-viewport centered card. Consistent with the Login
  // page so the consent screen feels like part of the sign-in flow,
  // not an in-shell settings page.
  const wrapperStyle: React.CSSProperties = {
    minHeight: "100vh",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    padding: token.paddingLG,
    background: token.colorBgLayout,
  };

  if (loading) {
    return (
      <div style={wrapperStyle}>
        <Spin size="large" />
      </div>
    );
  }

  if (loadError) {
    return (
      <div style={wrapperStyle}>
        <Card style={{ maxWidth: 480, width: "100%" }}>
          <Alert
            type="error"
            showIcon
            message="Could not load consent request"
            description={loadError}
          />
        </Card>
      </div>
    );
  }

  if (!metadata) {
    // Defensive branch — shouldn't happen given the error path above.
    return null;
  }

  return (
    <div style={wrapperStyle}>
      <Card
        style={{ maxWidth: 520, width: "100%" }}
        title={
          <Space>
            <LockOutlined />
            <span>Authorize {metadata.client_name}</span>
          </Space>
        }
      >
        <Paragraph>
          <Text strong>{metadata.client_name}</Text> is asking to access parts
          of your Jabali account. Review what you're approving below.
        </Paragraph>

        <Paragraph type="secondary" style={{ marginTop: token.marginSM }}>
          Signed in as <Text code>{metadata.subject}</Text>
        </Paragraph>

        <Title level={5} style={{ marginTop: token.marginLG }}>
          This app will be able to:
        </Title>

        <Space
          direction="vertical"
          size="small"
          style={{
            width: "100%",
            marginBottom: token.marginLG,
          }}
        >
          {metadata.requested_scope.map((s) => (
            <Card
              key={s.scope}
              size="small"
              style={{ background: token.colorFillQuaternary }}
            >
              <Text strong>{s.short}</Text>
              <Paragraph
                type="secondary"
                style={{ marginBottom: 0, marginTop: token.marginXXS }}
              >
                {s.long}
              </Paragraph>
            </Card>
          ))}
        </Space>

        {submitError && (
          <Alert
            type="error"
            showIcon
            message={submitError}
            style={{ marginBottom: token.marginMD }}
          />
        )}

        <Space style={{ width: "100%", justifyContent: "flex-end" }}>
          <Button
            icon={<CloseOutlined />}
            onClick={handleDeny}
            loading={submitting === "deny"}
            disabled={submitting !== null}
          >
            Deny
          </Button>
          <Button
            type="primary"
            icon={<CheckOutlined />}
            onClick={handleAccept}
            loading={submitting === "accept"}
            disabled={submitting !== null}
          >
            Allow
          </Button>
        </Space>
      </Card>
    </div>
  );
}
