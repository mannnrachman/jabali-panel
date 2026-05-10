// CreateMigrationDrawer — operator-driven 'New Migration' wizard.
// Step 1: pick source kind / host / user and create the job.
// Step 2+: source-specific drive steps (upload / pull / import) — no CLI needed.
import { useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Form,
  Input,
  Select,
  Space,
  Steps,
  Tabs,
  Typography,
  Upload,
  message,
} from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";
import { UploadOutlined } from "../../../icons";

// ─── types ────────────────────────────────────────────────────────────────────

type CreateInput = {
  source_kind: string;
  source_host: string;
  source_user: string;
};

type MigrationJob = {
  id: string;
  source_kind: string;
  source_host: string;
  source_user: string;
  state: string;
};

type SecretsInput = { ssh_password: string; ssh_private_key: string };
type PullInput = { ssh_user: string };
type ImportInput = {
  target_user: string;
  target_email: string;
  target_password: string;
  target_package_id: string;
};

// 'done' is shared terminal step for all flows
type CPanelStep = "secrets" | "pull" | "import" | "done";
type WHMStep = "tarball" | "import" | "done";
type DriveStep = CPanelStep | WHMStep;

// ─── source options ────────────────────────────────────────────────────────────

const SOURCE_OPTIONS = [
  { value: "cpanel", label: "cPanel (live SSH source)" },
  { value: "whm_pkgacct", label: "WHM pkgacct (uploaded tarball)" },
  { value: "directadmin", label: "DirectAdmin (Discoverer scaffold only)" },
  { value: "hestiacp", label: "HestiaCP (Discoverer scaffold only)" },
  { value: "imap_only", label: "IMAP-only (not yet wired)" },
];

// ─── sub-step components ───────────────────────────────────────────────────────

function SecretsStep({ jobId, onDone }: { jobId: string; onDone: () => void }) {
  const [form] = Form.useForm<SecretsInput>();
  const [credKind, setCredKind] = useState<"password" | "key">("password");

  const mut = useMutation({
    mutationFn: async (vals: SecretsInput) => {
      await apiClient.post(`/admin/migrations/${jobId}/secrets`, vals);
    },
    onSuccess: () => {
      message.success("SSH credentials saved");
      onDone();
    },
    onError: (err: unknown) => {
      const detail = (err as { response?: { data?: { detail?: string } } })
        ?.response?.data?.detail;
      message.error(detail ?? "Failed to save credentials");
    },
  });

  return (
    <Form form={form} layout="vertical" onFinish={(v) => void mut.mutateAsync(v)}>
      <Alert
        type="info"
        showIcon
        message="SSH credentials for the source server"
        description="Credentials are stored encrypted, used only to pull the account tarball, then permanently deleted when the job reaches a terminal state."
        style={{ marginBottom: 16 }}
      />
      <Tabs
        activeKey={credKind}
        onChange={(k) => {
          setCredKind(k as typeof credKind);
          form.resetFields();
        }}
        items={[
          {
            key: "password",
            label: "Password",
            children: (
              <Form.Item
                name="ssh_password"
                label="SSH password"
                rules={credKind === "password" ? [{ required: true, message: "Password required" }] : []}
              >
                <Input.Password placeholder="root password on source host" autoComplete="off" />
              </Form.Item>
            ),
          },
          {
            key: "key",
            label: "Private key",
            children: (
              <Form.Item
                name="ssh_private_key"
                label="PEM private key"
                rules={credKind === "key" ? [{ required: true, message: "Private key required" }] : []}
              >
                <Input.TextArea
                  rows={6}
                  placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                  style={{ fontFamily: "monospace", fontSize: 12 }}
                />
              </Form.Item>
            ),
          },
        ]}
      />
      <Button type="primary" htmlType="submit" loading={mut.isPending}>
        Save credentials
      </Button>
    </Form>
  );
}

function PullStep({ jobId, onDone }: { jobId: string; onDone: () => void }) {
  const [form] = Form.useForm<PullInput>();

  const mut = useMutation({
    mutationFn: async (vals: PullInput) => {
      await apiClient.post(`/admin/migrations/${jobId}/pull-source`, vals);
    },
    onSuccess: () => {
      message.success("Pull started — running in the background");
      onDone();
    },
    onError: (err: unknown) => {
      const detail = (err as { response?: { data?: { detail?: string } } })
        ?.response?.data?.detail;
      message.error(detail ?? "Failed to start pull");
    },
  });

  return (
    <Form
      form={form}
      layout="vertical"
      onFinish={(v) => void mut.mutateAsync(v)}
      initialValues={{ ssh_user: "root" }}
    >
      <Alert
        type="info"
        showIcon
        message="Pull account files from source"
        description="Triggers jabali migrate pull-source on the server. It SSH-connects to the source, downloads the cpmove tarball to the staging directory, then unpacks it. Check the job's stage timeline for live progress."
        style={{ marginBottom: 16 }}
      />
      <Form.Item
        name="ssh_user"
        label="SSH user"
        tooltip="Login used to connect to the source server. Usually 'root'."
      >
        <Input placeholder="root" />
      </Form.Item>
      <Button type="primary" htmlType="submit" loading={mut.isPending}>
        Start pull
      </Button>
    </Form>
  );
}

function TarballStep({ jobId, onDone }: { jobId: string; onDone: () => void }) {
  const [uploading, setUploading] = useState(false);

  const doUpload = async (file: File) => {
    setUploading(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      await apiClient.post(`/admin/migrations/${jobId}/tarball`, fd, {
        headers: { "Content-Type": "multipart/form-data" },
      });
      message.success("Tarball uploaded successfully");
      onDone();
    } catch (err: unknown) {
      const detail = (err as { response?: { data?: { detail?: string; error?: string } } })
        ?.response?.data?.detail;
      message.error(detail ?? "Upload failed");
    } finally {
      setUploading(false);
    }
  };

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="Upload the WHM pkgacct archive"
        description="Select the cpmove-<user>.tar.gz file produced by WHM's Full Backup / pkgacct tool. Files up to 20 GB are streamed directly — no intermediate buffering."
      />
      <Upload
        accept=".gz,.tar.gz"
        maxCount={1}
        showUploadList={false}
        beforeUpload={(file) => {
          void doUpload(file as unknown as File);
          return false;
        }}
      >
        <Button icon={<UploadOutlined />} loading={uploading}>
          {uploading ? "Uploading…" : "Select cpmove tarball (.tar.gz)"}
        </Button>
      </Upload>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        File name should match the pattern <Typography.Text code style={{ fontSize: 12 }}>cpmove-&lt;user&gt;.tar.gz</Typography.Text>
      </Typography.Text>
    </Space>
  );
}

function ImportStep({ jobId, onDone }: { jobId: string; onDone: () => void }) {
  const [form] = Form.useForm<ImportInput>();

  const mut = useMutation({
    mutationFn: async (vals: ImportInput) => {
      await apiClient.post(`/admin/migrations/${jobId}/import`, vals);
    },
    onSuccess: () => {
      message.success("Import started — running in the background");
      onDone();
    },
    onError: (err: unknown) => {
      const detail = (err as { response?: { data?: { detail?: string } } })
        ?.response?.data?.detail;
      message.error(detail ?? "Failed to start import");
    },
  });

  return (
    <Form form={form} layout="vertical" onFinish={(v) => void mut.mutateAsync(v)}>
      <Alert
        type="info"
        showIcon
        message="Set destination account"
        description="Specify the local username to import into. If the user doesn't exist yet, add email + password to auto-create them. Target package ID is optional — the default package is used when blank."
        style={{ marginBottom: 16 }}
      />
      <Form.Item
        name="target_user"
        label="Target username"
        rules={[{ required: true, message: "Username is required" }]}
        tooltip="The local Jabali username the account will be imported into."
      >
        <Input placeholder="alice" />
      </Form.Item>
      <Form.Item name="target_email" label="Email (for new-user auto-creation)">
        <Input placeholder="alice@example.com" />
      </Form.Item>
      <Form.Item name="target_password" label="Password (for new-user auto-creation)">
        <Input.Password placeholder="leave blank to skip auto-create" autoComplete="off" />
      </Form.Item>
      <Form.Item name="target_package_id" label="Package ID (optional)">
        <Input placeholder="leave blank for default package" />
      </Form.Item>
      <Button type="primary" htmlType="submit" loading={mut.isPending}>
        Start import
      </Button>
    </Form>
  );
}

// ─── steps config ─────────────────────────────────────────────────────────────

const CPANEL_STEPS: { title: string; step: CPanelStep | "create" }[] = [
  { title: "Create job", step: "create" },
  { title: "SSH credentials", step: "secrets" },
  { title: "Pull source", step: "pull" },
  { title: "Run import", step: "import" },
];

const WHM_STEPS: { title: string; step: WHMStep | "create" }[] = [
  { title: "Create job", step: "create" },
  { title: "Upload tarball", step: "tarball" },
  { title: "Run import", step: "import" },
];

function stepIndex(kind: string, current: DriveStep | "create"): number {
  const steps = kind === "whm_pkgacct" ? WHM_STEPS : CPANEL_STEPS;
  const idx = steps.findIndex((s) => s.step === current);
  return idx >= 0 ? idx : 0;
}

function stepsItems(kind: string, current: DriveStep | "create") {
  const steps = kind === "whm_pkgacct" ? WHM_STEPS : CPANEL_STEPS;
  const cur = stepIndex(kind, current);
  return steps.map((s, i) => ({
    title: s.title,
    status: (i < cur ? "finish" : i === cur ? "process" : "wait") as
      | "finish"
      | "process"
      | "wait",
  }));
}

// ─── main drawer ───────────────────────────────────────────────────────────────

export interface CreateMigrationDrawerProps {
  open: boolean;
  onClose: () => void;
}

export const CreateMigrationDrawer = ({
  open,
  onClose,
}: CreateMigrationDrawerProps) => {
  const [form] = Form.useForm<CreateInput>();
  const qc = useQueryClient();
  const [created, setCreated] = useState<MigrationJob | null>(null);
  const [driveStep, setDriveStep] = useState<DriveStep>("secrets");

  const create = useMutation<MigrationJob, unknown, CreateInput>({
    mutationFn: async (input) => {
      const { data } = await apiClient.post<MigrationJob>(
        "/admin/migrations",
        input,
      );
      return data;
    },
    onSuccess: async (job) => {
      message.success("Migration job created");
      setCreated(job);
      setDriveStep(job.source_kind === "whm_pkgacct" ? "tarball" : "secrets");
      await qc.invalidateQueries({ queryKey: ["admin-migrations"] });
    },
    onError: (err) => {
      const detail =
        (err as { response?: { data?: { error?: string; detail?: string } } })
          ?.response?.data?.detail;
      message.error(detail ?? "Create failed");
    },
  });

  const handleSubmit = async (values: CreateInput) => {
    await create.mutateAsync(values);
  };

  const handleDone = () => {
    form.resetFields();
    setCreated(null);
    setDriveStep("secrets");
    onClose();
  };

  const isScaffoldOnly =
    created &&
    ["directadmin", "hestiacp", "imap_only"].includes(created.source_kind);

  return (
    <Drawer
      title="New migration job"
      open={open}
      onClose={handleDone}
      width={640}
      destroyOnClose
    >
      {!created ? (
        // ── Step 1: create form ──────────────────────────────────────────────
        <Form<CreateInput>
          form={form}
          layout="vertical"
          onFinish={handleSubmit}
          initialValues={{ source_kind: "cpanel" }}
        >
          <Form.Item
            label="Source kind"
            name="source_kind"
            rules={[{ required: true, message: "Pick a source panel kind" }]}
          >
            <Select options={SOURCE_OPTIONS} />
          </Form.Item>
          <Form.Item
            label="Source host"
            name="source_host"
            tooltip="Hostname of the source panel (e.g. src.example.com). Not required for WHM tarball uploads."
          >
            <Input placeholder="src.example.com" />
          </Form.Item>
          <Form.Item
            label="Source user"
            name="source_user"
            rules={[{ required: true, message: "Source-side username required" }]}
            tooltip="Login name on the source panel. cPanel: typically lowercase, 1–16 chars."
          >
            <Input placeholder="bob" />
          </Form.Item>

          <Space>
            <Button
              type="primary"
              htmlType="submit"
              loading={create.isPending}
            >
              Create job
            </Button>
            <Button onClick={handleDone}>Cancel</Button>
          </Space>
        </Form>
      ) : isScaffoldOnly ? (
        // ── Scaffold-only: no UI drive steps yet ─────────────────────────────
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            message={`Job created: ${created.id}`}
          />
          <Alert
            type="warning"
            showIcon
            message="UI-driven import not yet available for this source type"
            description={
              <Typography.Paragraph style={{ marginBottom: 0 }}>
                DirectAdmin, HestiaCP, and IMAP-only sources are scaffold-only
                in this release. To import, use the CLI:{" "}
                <Typography.Text code style={{ fontSize: 12 }}>
                  jabali migrate import --job-id {created.id} --target-user &lt;user&gt;
                </Typography.Text>
              </Typography.Paragraph>
            }
          />
          <Button type="primary" onClick={handleDone}>
            Done
          </Button>
        </Space>
      ) : (
        // ── Drive wizard: secrets → pull → import (cPanel)
        //                  tarball → import (WHM) ────────────────────────────
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            message={`Job created: ${created.id}`}
            description={`Source: ${created.source_host || "—"}  ·  User: ${created.source_user}`}
          />

          {driveStep !== "done" && (
            <Steps
              size="small"
              current={stepIndex(created.source_kind, driveStep)}
              items={stepsItems(created.source_kind, driveStep)}
            />
          )}

          {driveStep === "secrets" && (
            <SecretsStep jobId={created.id} onDone={() => setDriveStep("pull")} />
          )}

          {driveStep === "pull" && (
            <PullStep jobId={created.id} onDone={() => setDriveStep("import")} />
          )}

          {driveStep === "tarball" && (
            <TarballStep jobId={created.id} onDone={() => setDriveStep("import")} />
          )}

          {driveStep === "import" && (
            <ImportStep jobId={created.id} onDone={() => setDriveStep("done")} />
          )}

          {driveStep === "done" && (
            <Space direction="vertical" size="middle" style={{ width: "100%" }}>
              <Alert
                type="success"
                showIcon
                message="Import started"
                description="The migration is running in the background. Open the migration job to track stage-by-stage progress."
              />
              <Button type="primary" onClick={handleDone}>
                Done
              </Button>
            </Space>
          )}
        </Space>
      )}
    </Drawer>
  );
};
