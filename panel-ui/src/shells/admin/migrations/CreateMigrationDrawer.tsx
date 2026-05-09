// CreateMigrationDrawer — operator-driven 'New Migration' form.
// Inserts a migration_jobs row with state='pending'. Does NOT
// trigger the runner — operator runs `jabali migrate import` in a
// shell after dropping the cpmove tarball under
// /var/lib/jabali-migrations/<job-id>/extracted/. Drawer surfaces
// the resulting job ID + a copy-pasteable CLI command so the
// operator's next step is one click away.
import { useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Form,
  Input,
  Select,
  Space,
  Typography,
  message,
} from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";

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

const SOURCE_OPTIONS = [
  { value: "cpanel", label: "cPanel (live SSH source)" },
  { value: "whm_pkgacct", label: "WHM pkgacct (uploaded tarball)" },
  { value: "directadmin", label: "DirectAdmin (Discoverer scaffold only)" },
  { value: "hestiacp", label: "HestiaCP (Discoverer scaffold only)" },
  { value: "imap_only", label: "IMAP-only (not yet wired)" },
];

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
    onClose();
  };

  return (
    <Drawer
      title="New migration job"
      open={open}
      onClose={handleDone}
      width={620}
      destroyOnClose
    >
      {!created ? (
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
            tooltip="Source panel hostname (e.g. src.example.com). Optional for whm_pkgacct uploads."
          >
            <Input placeholder="src.example.com" />
          </Form.Item>
          <Form.Item
            label="Source user"
            name="source_user"
            rules={[{ required: true, message: "Source-side username required" }]}
            tooltip="Login name on the source panel. cPanel: typically lowercase 1-16 chars."
          >
            <Input placeholder="bob" />
          </Form.Item>

          <Alert
            type="info"
            showIcon
            message="What this does"
            description={
              <Typography.Paragraph style={{ marginBottom: 0 }}>
                Inserts a migration_jobs row with state=pending. The runner is
                NOT auto-triggered — drop the cpmove tarball under{" "}
                <Typography.Text code>
                  /var/lib/jabali-migrations/&lt;job-id&gt;/
                </Typography.Text>{" "}
                + run{" "}
                <Typography.Text code>jabali migrate import</Typography.Text>{" "}
                in a shell to start the actual migration. See runbook §2.3.
              </Typography.Paragraph>
            }
            style={{ marginBottom: 16 }}
          />

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
      ) : (
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            message={`Job created: ${created.id}`}
            description={
              <Typography.Paragraph style={{ marginBottom: 0 }}>
                Next step — drop the cpmove tarball + run the importer in a
                shell. See the runbook §2.3 for tarball pull / extraction.
              </Typography.Paragraph>
            }
          />

          <Card title="Step 1 — extract tarball" />
          <Typography.Paragraph>
            <Typography.Text code style={{ fontSize: 12, whiteSpace: "pre-wrap" }}>
              {`mkdir -p /var/lib/jabali-migrations/${created.id}/extracted
scp root@${created.source_host || "<source-host>"}:/home/cpmove-${created.source_user}.tar.gz \\
    /var/lib/jabali-migrations/${created.id}/
tar -xzf /var/lib/jabali-migrations/${created.id}/cpmove-${created.source_user}.tar.gz \\
    -C /var/lib/jabali-migrations/${created.id}/extracted/`}
            </Typography.Text>
          </Typography.Paragraph>

          <Card title="Step 2 — run importer" />
          <Typography.Paragraph>
            <Typography.Text code style={{ fontSize: 12, whiteSpace: "pre-wrap" }}>
              {`jabali migrate import \\
  --job-id ${created.id} \\
  --target-user <username> \\
  --target-email <email> \\
  --target-password <password>`}
            </Typography.Text>
          </Typography.Paragraph>

          <Space>
            <Button type="primary" onClick={handleDone}>
              Done
            </Button>
          </Space>
        </Space>
      )}
    </Drawer>
  );
};

const Card = ({ title }: { title: string }) => (
  <Typography.Title level={5} style={{ margin: 0 }}>
    {title}
  </Typography.Title>
);
