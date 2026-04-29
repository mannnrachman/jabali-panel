// BackupSettingsTab — knobs that govern the in-process backup
// scheduler/dispatcher. Backed by server_settings (PATCH /admin/settings).
import { Button, Form, InputNumber, Spin, Typography, message } from "antd";
import { SaveOutlined } from "@icons";
import { useEffect, useState } from "react";

import { apiClient } from "../../../apiClient";
import { extractApiError } from "../../../apiErrors";

interface BackupSettingsShape {
  backup_max_concurrent_jobs: number;
  backup_keep_daily: number;
  backup_keep_weekly: number;
  backup_keep_monthly: number;
}

interface ServerSettingsResponse {
  backup_max_concurrent_jobs?: number;
  backup_keep_daily?: number;
  backup_keep_weekly?: number;
  backup_keep_monthly?: number;
}

export const BackupSettingsTab = () => {
  const [form] = Form.useForm<BackupSettingsShape>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await apiClient.get<ServerSettingsResponse>("/admin/settings");
        if (cancelled) return;
        form.setFieldsValue({
          backup_max_concurrent_jobs: resp.data.backup_max_concurrent_jobs ?? 2,
          backup_keep_daily: resp.data.backup_keep_daily ?? 7,
          backup_keep_weekly: resp.data.backup_keep_weekly ?? 4,
          backup_keep_monthly: resp.data.backup_keep_monthly ?? 6,
        });
      } catch (err) {
        message.error(extractApiError(err, "Load failed"));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [form]);

  const handleSubmit = async (values: BackupSettingsShape) => {
    setSaving(true);
    try {
      await apiClient.patch("/admin/settings", values);
      message.success("Settings saved");
    } catch (err) {
      message.error(extractApiError(err, "Save failed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Spin spinning={loading}>
      <Form<BackupSettingsShape>
        form={form}
        layout="vertical"
        onFinish={handleSubmit}
        style={{ maxWidth: 480 }}
      >
        <Form.Item
          name="backup_max_concurrent_jobs"
          label="Max concurrent backup jobs"
          tooltip="The dispatcher runs at most this many backup_jobs at once. Scheduled jobs queue and start as slots free."
          rules={[{ required: true, type: "number", min: 1, max: 64 }]}
        >
          <InputNumber min={1} max={64} style={{ width: "100%" }} />
        </Form.Item>

        <Typography.Title level={5} style={{ marginTop: 24 }}>
          Retention (restic forget)
        </Typography.Title>
        <Form.Item
          name="backup_keep_daily"
          label="Keep daily"
          rules={[{ required: true, type: "number", min: 0, max: 365 }]}
        >
          <InputNumber min={0} max={365} style={{ width: "100%" }} />
        </Form.Item>
        <Form.Item
          name="backup_keep_weekly"
          label="Keep weekly"
          rules={[{ required: true, type: "number", min: 0, max: 104 }]}
        >
          <InputNumber min={0} max={104} style={{ width: "100%" }} />
        </Form.Item>
        <Form.Item
          name="backup_keep_monthly"
          label="Keep monthly"
          rules={[{ required: true, type: "number", min: 0, max: 120 }]}
        >
          <InputNumber min={0} max={120} style={{ width: "100%" }} />
        </Form.Item>

        <Form.Item>
          <Button type="primary" htmlType="submit" loading={saving} icon={<SaveOutlined />}>
            Save
          </Button>
        </Form.Item>
      </Form>
    </Spin>
  );
};
