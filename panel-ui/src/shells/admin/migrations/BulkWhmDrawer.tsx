// BulkWhmDrawer — ADR-0095 decision 3. Creates N draft migration_jobs
// in one shot, all sharing a batch_id. Used for the WHM "migrate every
// account on this server" workflow; pairs with the existing
// CreateMigrationDrawer which still handles single-account flows.
//
// v1 surface: paste accounts as newline / comma-separated list. The
// M35.2 follow-up replaces the textarea with a discover-accounts call
// that returns the cPanel account list from a live WHM API session.
import { useState } from "react";
import {
  Alert,
  Button,
  Drawer,
  Form,
  Input,
  Space,
  Typography,
  message,
} from "antd";
import { useMutation } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";

type BulkResp = {
  batch_id: string;
  jobs: Array<{ id: string; source_user: string }>;
};

interface Props {
  open: boolean;
  onClose: () => void;
  onCreated?: (batchID: string) => void;
}

export const BulkWhmDrawer = ({ open, onClose, onCreated }: Props) => {
  const [form] = Form.useForm<{ source_host: string; accounts: string }>();
  const [result, setResult] = useState<BulkResp | null>(null);

  const mut = useMutation({
    mutationFn: async (vals: { source_host: string; accounts: string }) => {
      // Accept newline OR comma OR space separated lists; trim each
      // entry, drop empties. Server-side bulk endpoint silently skips
      // duplicate (host, user, kind) tuples so re-submission after a
      // partial run is idempotent.
      const accounts = vals.accounts
        .split(/[\s,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      const { data } = await apiClient.post<BulkResp>("/admin/migrations/bulk", {
        source_kind: "whm_pkgacct",
        source_host: vals.source_host,
        accounts,
      });
      return data;
    },
    onSuccess: (data) => {
      setResult(data);
      message.success(`Batch ${data.batch_id} — ${data.jobs.length} jobs queued`);
      onCreated?.(data.batch_id);
    },
    onError: (e: unknown) => {
      const detail = (e as { response?: { data?: { detail?: string } } })?.response
        ?.data?.detail;
      message.error(detail ?? "Bulk create failed");
    },
  });

  const handleDone = () => {
    setResult(null);
    form.resetFields();
    onClose();
  };

  return (
    <Drawer
      open={open}
      onClose={handleDone}
      width={520}
      title="Create WHM migration batch"
      destroyOnClose
    >
      {!result ? (
        <Form
          form={form}
          layout="vertical"
          onFinish={(vals) => mut.mutate(vals)}
        >
          <Alert
            type="info"
            showIcon
            message="Bulk WHM"
            description="Creates one migration_job per account, all sharing a batch_id. Cancel-batch + per-account retry are available from the list page."
          />
          <Form.Item
            label="Source host"
            name="source_host"
            rules={[{ required: true, message: "Source WHM host required" }]}
            tooltip="Hostname or IP of the source WHM server. Private-IP targets require server_settings.migration_allow_private_hosts=true."
          >
            <Input placeholder="src.example.com" />
          </Form.Item>
          <Form.Item
            label="Accounts"
            name="accounts"
            rules={[{ required: true, message: "At least one account required" }]}
            tooltip="One account per line, or comma-separated. Existing jobs for any (host, account) are skipped automatically."
          >
            <Input.TextArea
              rows={8}
              placeholder="alice&#10;bob&#10;charlie"
              autoSize={{ minRows: 6, maxRows: 16 }}
            />
          </Form.Item>
          <Space>
            <Button type="primary" htmlType="submit" loading={mut.isPending}>
              Create batch
            </Button>
            <Button onClick={handleDone}>Cancel</Button>
          </Space>
        </Form>
      ) : (
        <Space direction="vertical" size="large" style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            message={`Batch ${result.batch_id} created`}
            description={`${result.jobs.length} migration_jobs queued in draft state. Configure secrets + pull + import per job from the migrations list.`}
          />
          <Typography.Text type="secondary">
            Cancel the entire batch with DELETE /admin/migrations/batches/{result.batch_id}.
          </Typography.Text>
          <Button type="primary" onClick={handleDone}>
            Done
          </Button>
        </Space>
      )}
    </Drawer>
  );
};
