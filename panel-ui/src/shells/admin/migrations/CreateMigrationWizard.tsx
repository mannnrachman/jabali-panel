// CreateMigrationWizard — ADR-0095 decisions 1+3+5+6.
//
// 4-step flow:
//   1. Source kind (cPanel | DirectAdmin | HestiaCP | WHM pkgacct)
//   2. Connection: host + admin user + ingest (live SSH vs cpmove upload
//      for cpanel; live only for the others). Secrets POSTed to /:id/secrets.
//   3. (WHM only) Account discovery + multi-select picker. Other source
//      kinds skip this step.
//   4. Review + submit. For WHM the picker → POST /bulk creates one
//      job per selected account; the wizard's own draft row stays as
//      the "configuration template" and is destroyed at success.
//
// Wizard state persistence (decision 5): the draft row is the source of
// truth. Browser refresh loads the row by URL ?wizard=<id>; closes the
// drawer otherwise. Drafts older than 24h are reaped by the secrets
// reaper timer.
//
// Single-account flow stays in CreateMigrationDrawer for now —
// migrating it into the wizard is M35.2 work.
import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Drawer,
  Form,
  Input,
  Radio,
  Space,
  Spin,
  Steps,
  Typography,
  message,
} from "antd";
import { useMutation, useQuery } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";
import { humanBytes } from "../../../utils/bytes";

type DraftJob = {
  id: string;
  source_kind: string;
  source_host: string;
  source_user: string;
  state: string;
};

type DiscoveredAccount = {
  id: string;
  login: string;
  domain?: string;
  email?: string;
  bytes_total: number;
  suspended?: boolean;
};

const SOURCE_OPTIONS = [
  { value: "whm_pkgacct", label: "WHM (bulk: many cPanel accounts)" },
  { value: "cpanel", label: "cPanel (single account)" },
  { value: "directadmin", label: "DirectAdmin" },
  { value: "hestiacp", label: "HestiaCP" },
];

interface Props {
  open: boolean;
  onClose: () => void;
  onCreated?: (batchID: string | null) => void;
}

export const CreateMigrationWizard = ({ open, onClose, onCreated }: Props) => {
  const [step, setStep] = useState(0);
  const [draftId, setDraftId] = useState<string | null>(null);
  const [sourceKind, setSourceKind] = useState<string>("whm_pkgacct");
  const [sourceHost, setSourceHost] = useState<string>("");
  const [sourceUser, setSourceUser] = useState<string>("");
  const [credKind, setCredKind] = useState<"password" | "key">("password");
  const [credValue, setCredValue] = useState<string>("");
  const [selected, setSelected] = useState<Set<string>>(new Set());

  // ADR-0095 decision 5 — wizard URL persistence. When the drawer
  // opens with ?wizard=<id> in the URL, fetch the draft row and
  // restore state. The operator's mid-wizard tab refresh resumes
  // where they left off; new wizards leave the param absent.
  useEffect(() => {
    if (!open) return;
    if (draftId) return;
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    const w = url.searchParams.get("wizard");
    if (!w) return;
    void apiClient
      .get<DraftJob>(`/admin/migrations/${w}`)
      .then((r) => {
        if (r.data.state === "draft") {
          setDraftId(r.data.id);
          setSourceKind(r.data.source_kind);
          if (!r.data.source_host.startsWith("__draft_")) {
            setSourceHost(r.data.source_host);
          }
          if (!r.data.source_user.startsWith("__draft_")) {
            setSourceUser(r.data.source_user);
          }
          setStep(1); // jump past source-kind to connection step
        }
      })
      .catch(() => {
        // Drop the param — stale link from a reaped draft.
        url.searchParams.delete("wizard");
        window.history.replaceState({}, "", url.toString());
      });
  }, [open, draftId]);

  const reset = () => {
    setStep(0);
    setDraftId(null);
    setSourceKind("whm_pkgacct");
    setSourceHost("");
    setSourceUser("");
    setCredKind("password");
    setCredValue("");
    setSelected(new Set());
  };

  // ── Step 1 → 2: create draft row ────────────────────────────────────
  const createDraft = useMutation({
    mutationFn: async () => {
      const { data } = await apiClient.post<DraftJob>("/admin/migrations", {
        source_kind: sourceKind,
        source_host: sourceHost || `__draft_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`,
        source_user: sourceUser || `__draft_${Math.random().toString(36).slice(2, 10)}`,
        state: "draft",
      });
      return data;
    },
    onSuccess: (d) => {
      setDraftId(d.id);
      setStep(1);
      // ADR-0095 decision 5 — URL deep-link so a tab refresh mid-
      // wizard restores the draft instead of starting over. Read at
      // mount via useSearchParams (added below).
      if (typeof window !== "undefined") {
        const url = new URL(window.location.href);
        url.searchParams.set("wizard", d.id);
        window.history.replaceState({}, "", url.toString());
      }
    },
    onError: (e: unknown) => {
      message.error(
        (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail ??
          "Draft create failed",
      );
    },
  });

  // ── Step 2 → 3: PATCH draft + upload secrets ────────────────────────
  const submitConnection = useMutation({
    mutationFn: async () => {
      if (!draftId) throw new Error("no draft");
      await apiClient.patch(`/admin/migrations/${draftId}`, {
        source_host: sourceHost,
        source_user: sourceUser,
      });
      const body: Record<string, string> =
        credKind === "password"
          ? { password: credValue }
          : { private_key: credValue };
      await apiClient.post(`/admin/migrations/${draftId}/secrets`, body);
    },
    onSuccess: () => {
      // WHM goes to account picker; others skip straight to summary.
      setStep(sourceKind === "whm_pkgacct" ? 2 : 3);
    },
    onError: (e: unknown) => {
      message.error(
        (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail ??
          "Connection step failed",
      );
    },
  });

  // ── Step 3 (WHM): discover accounts ─────────────────────────────────
  const accounts = useQuery<{ accounts: DiscoveredAccount[] }>({
    queryKey: ["wizard", "discover", draftId],
    queryFn: async () => {
      const { data } = await apiClient.get(
        `/admin/migrations/${draftId}/discover-accounts`,
      );
      return data;
    },
    enabled: step === 2 && !!draftId,
    retry: false,
  });

  // ── Step 4: bulk create from selection (WHM) ────────────────────────
  const bulk = useMutation({
    mutationFn: async () => {
      const { data } = await apiClient.post<{ batch_id: string }>(
        "/admin/migrations/bulk",
        {
          source_kind: "whm_pkgacct",
          source_host: sourceHost,
          accounts: [...selected],
        },
      );
      return data;
    },
    onSuccess: (d) => {
      message.success(`Batch ${d.batch_id.slice(-6)} queued`);
      onCreated?.(d.batch_id);
      handleClose();
    },
    onError: (e: unknown) => {
      message.error(
        (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail ??
          "Bulk create failed",
      );
    },
  });

  // ── single-account finalize: flip draft → pending via /:id/submit
  const finalize = useMutation({
    mutationFn: async () => {
      if (!draftId) throw new Error("no draft");
      await apiClient.post(`/admin/migrations/${draftId}/submit`);
    },
    onSuccess: () => {
      message.success("Migration submitted — runner picks it up on next tick.");
      onCreated?.(null);
      handleClose();
    },
    onError: (e: unknown) => {
      message.error(
        (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail ??
          "Submit failed",
      );
    },
  });

  const handleClose = () => {
    reset();
    if (typeof window !== "undefined") {
      const url = new URL(window.location.href);
      if (url.searchParams.has("wizard")) {
        url.searchParams.delete("wizard");
        window.history.replaceState({}, "", url.toString());
      }
    }
    onClose();
  };

  return (
    <Drawer
      open={open}
      onClose={handleClose}
      width={680}
      title="Create migration"
      destroyOnClose
    >
      <Steps
        current={step}
        size="small"
        style={{ marginBottom: 24 }}
        items={[
          { title: "Source" },
          { title: "Connection" },
          { title: sourceKind === "whm_pkgacct" ? "Accounts" : "Skip" },
          { title: "Review" },
        ]}
      />

      {step === 0 && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert
            type="info"
            showIcon
            message="Pick the source panel type"
            description="WHM enables bulk migration of every cPanel account. Single-account migrations are still available via the 'New migration' button."
          />
          <Radio.Group
            value={sourceKind}
            onChange={(e) => setSourceKind(e.target.value)}
            style={{ display: "flex", flexDirection: "column", gap: 8 }}
          >
            {SOURCE_OPTIONS.map((o) => (
              <Radio key={o.value} value={o.value}>
                {o.label}
              </Radio>
            ))}
          </Radio.Group>
          <Button
            type="primary"
            loading={createDraft.isPending}
            onClick={() => createDraft.mutate()}
          >
            Next: connection
          </Button>
        </Space>
      )}

      {step === 1 && draftId && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert
            type="info"
            showIcon
            message={`Draft ${draftId.slice(-6)} created`}
            description="Credentials are written to /etc/jabali-panel/migration-secrets and reaped 24h after job completion."
          />
          <Form layout="vertical">
            <Form.Item label="Source host" required>
              <Input
                value={sourceHost}
                onChange={(e) => setSourceHost(e.target.value)}
                placeholder="src.example.com"
              />
            </Form.Item>
            <Form.Item label="Admin user" required>
              <Input
                value={sourceUser}
                onChange={(e) => setSourceUser(e.target.value)}
                placeholder="root"
              />
            </Form.Item>
            <Form.Item label="Credential type">
              <Radio.Group value={credKind} onChange={(e) => setCredKind(e.target.value)}>
                <Radio value="password">Password</Radio>
                <Radio value="key">SSH key</Radio>
              </Radio.Group>
            </Form.Item>
            <Form.Item
              label={credKind === "password" ? "Password" : "Private key (PEM)"}
            >
              {credKind === "password" ? (
                <Input.Password
                  value={credValue}
                  onChange={(e) => setCredValue(e.target.value)}
                />
              ) : (
                <Input.TextArea
                  rows={6}
                  value={credValue}
                  onChange={(e) => setCredValue(e.target.value)}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                />
              )}
            </Form.Item>
          </Form>
          <Space>
            <Button onClick={() => setStep(0)}>Back</Button>
            <Button
              type="primary"
              loading={submitConnection.isPending}
              disabled={!sourceHost || !sourceUser || !credValue}
              onClick={() => submitConnection.mutate()}
            >
              {sourceKind === "whm_pkgacct" ? "Next: discover accounts" : "Next: review"}
            </Button>
          </Space>
        </Space>
      )}

      {step === 2 && draftId && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          {accounts.isLoading && (
            <div style={{ textAlign: "center", padding: 24 }}>
              <Spin tip="Discovering accounts on source server…" />
            </div>
          )}
          {accounts.error && (
            <Alert
              type="error"
              showIcon
              message="Account discovery failed"
              description={(accounts.error as Error).message}
            />
          )}
          {accounts.data && (
            <>
              <Alert
                type="success"
                showIcon
                message={`Found ${accounts.data.accounts.length} accounts`}
                description="Pick which accounts to migrate. Each becomes its own migration_job sharing a batch_id."
              />
              <Checkbox
                checked={selected.size === accounts.data.accounts.length}
                indeterminate={
                  selected.size > 0 &&
                  selected.size < accounts.data.accounts.length
                }
                onChange={(e) => {
                  if (e.target.checked) {
                    setSelected(new Set(accounts.data!.accounts.map((a) => a.login)));
                  } else {
                    setSelected(new Set());
                  }
                }}
              >
                Select all
              </Checkbox>
              <div style={{ maxHeight: 360, overflowY: "auto", padding: 8, border: "1px solid #d9d9d9", borderRadius: 4 }}>
                {accounts.data.accounts.map((a) => (
                  <div key={a.login} style={{ padding: "4px 0" }}>
                    <Checkbox
                      checked={selected.has(a.login)}
                      onChange={(e) => {
                        const next = new Set(selected);
                        if (e.target.checked) next.add(a.login);
                        else next.delete(a.login);
                        setSelected(next);
                      }}
                    >
                      <Typography.Text code>{a.login}</Typography.Text>
                      {a.domain && (
                        <Typography.Text type="secondary" style={{ marginLeft: 8 }}>
                          {a.domain}
                        </Typography.Text>
                      )}
                      {a.bytes_total > 0 && (
                        <Typography.Text type="secondary" style={{ marginLeft: 8, fontVariantNumeric: "tabular-nums" }}>
                          {humanBytes(a.bytes_total)}
                        </Typography.Text>
                      )}
                      {a.suspended && (
                        <Typography.Text type="warning" style={{ marginLeft: 8 }}>
                          (suspended)
                        </Typography.Text>
                      )}
                    </Checkbox>
                  </div>
                ))}
              </div>
            </>
          )}
          <Space>
            <Button onClick={() => setStep(1)}>Back</Button>
            <Button
              type="primary"
              disabled={selected.size === 0}
              onClick={() => setStep(3)}
            >
              Next: review {selected.size} accounts
            </Button>
          </Space>
        </Space>
      )}

      {step === 3 && (
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Alert
            type="info"
            showIcon
            message="Review"
            description={
              <>
                <div>
                  <b>Source:</b> {sourceHost} ({sourceKind})
                </div>
                <div>
                  <b>Admin user:</b> {sourceUser}
                </div>
                <div>
                  <b>Accounts:</b>{" "}
                  {sourceKind === "whm_pkgacct"
                    ? `${selected.size} selected`
                    : "single account"}
                </div>
              </>
            }
          />
          <Space>
            <Button onClick={() => setStep(sourceKind === "whm_pkgacct" ? 2 : 1)}>
              Back
            </Button>
            {sourceKind === "whm_pkgacct" ? (
              <Button
                type="primary"
                loading={bulk.isPending}
                onClick={() => bulk.mutate()}
              >
                Create batch
              </Button>
            ) : (
              <Button
                type="primary"
                loading={finalize.isPending}
                onClick={() => finalize.mutate()}
              >
                Submit
              </Button>
            )}
          </Space>
        </Space>
      )}
    </Drawer>
  );
};
