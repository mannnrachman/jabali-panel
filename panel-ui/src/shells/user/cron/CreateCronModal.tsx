import { useState } from "react";
import {
  Modal,
  Form,
  Input,
  Radio,
  Button,
  Space,
  Divider,
  Typography,
} from "antd";
import { App } from "antd";
import {
  createCronJob,
  updateCronJob,
  type CronJob,
} from "../../../apiClient";

const SCHEDULE_PRESETS = [
  { label: "Hourly", value: "0 * * * *" },
  { label: "Daily at 3 AM", value: "0 3 * * *" },
  { label: "Weekly (Sun 3 AM)", value: "0 3 * * 0" },
  { label: "Monthly (1st 3 AM)", value: "0 3 1 * *" },
  { label: "Advanced", value: "advanced" },
];

interface CreateCronModalProps {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
  initial?: CronJob | null;
}

export const CreateCronModal = ({
  open,
  onClose,
  onSuccess,
  initial,
}: CreateCronModalProps) => {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [scheduleMode, setScheduleMode] = useState<string>(
    initial ? (initial.schedule in SCHEDULE_PRESETS.map((p) => p.value) ? initial.schedule : "advanced") : "0 * * * *"
  );
  const [customSchedule, setCustomSchedule] = useState(
    initial && !SCHEDULE_PRESETS.map((p) => p.value).includes(initial.schedule)
      ? initial.schedule
      : ""
  );
  const { message: antMessage } = App.useApp();

  const isEditing = !!initial;

  const handleSubmit = async (values: {
    name: string;
    command: string;
  }) => {
    setLoading(true);
    try {
      const schedule = scheduleMode === "advanced" ? customSchedule : scheduleMode;

      if (!schedule || schedule.trim() === "") {
        antMessage.error("Please enter a schedule");
        setLoading(false);
        return;
      }

      if (isEditing && initial) {
        await updateCronJob(initial.id, {
          name: values.name,
          command: values.command,
          schedule,
        });
        antMessage.success("Cron job updated successfully");
      } else {
        await createCronJob({
          name: values.name,
          command: values.command,
          schedule,
        });
        antMessage.success("Cron job created successfully");
      }

      form.resetFields();
      setScheduleMode("0 * * * *");
      setCustomSchedule("");
      onSuccess();
    } catch (error: unknown) {
      const err = error as any;
      const detail = err?.response?.data?.detail;
      const code = err?.response?.data?.error;

      if (detail) {
        // Map field errors from backend detail
        if (detail.includes("command")) {
          form.setFields([
            {
              name: "command",
              errors: [detail],
            },
          ]);
          antMessage.error("Invalid command");
        } else if (detail.includes("schedule")) {
          antMessage.error("Invalid schedule: " + detail);
        } else {
          antMessage.error(detail);
        }
      } else if (code) {
        antMessage.error(code.replace(/_/g, " "));
      } else {
        const msg = err?.message ?? "Failed to save cron job";
        antMessage.error(msg);
      }
    } finally {
      setLoading(false);
    }
  };

  const handleModalClose = () => {
    form.resetFields();
    setScheduleMode(initial?.schedule || "0 * * * *");
    setCustomSchedule("");
    onClose();
  };

  return (
    <Modal
      title={isEditing ? "Edit Cron Job" : "Create Cron Job"}
      open={open}
      onCancel={handleModalClose}
      footer={null}
      width={600}
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={handleSubmit}
        initialValues={{
          name: initial?.name || "",
          command: initial?.command || "",
        }}
      >
        <Form.Item
          label="Name"
          name="name"
          rules={[
            { required: true, message: "Please enter a job name" },
            { max: 100, message: "Name must be 100 characters or less" },
          ]}
        >
          <Input placeholder="e.g., WordPress Cron Cleanup" />
        </Form.Item>

        <Form.Item
          label="Command"
          name="command"
          rules={[
            { required: true, message: "Please enter a command" },
          ]}
        >
          <Input.TextArea
            placeholder="wp cron event run --path=/home/user/example.com/public_html"
            rows={3}
            style={{ fontFamily: "monospace" }}
          />
        </Form.Item>

        <Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Must start with <code>wp</code> or <code>php</code>. Shell operators (|, &, $, ...) are rejected.
          </Typography.Text>
        </Form.Item>

        <Divider style={{ margin: "16px 0" }} />

        <Form.Item label="Schedule">
          <Radio.Group
            value={scheduleMode}
            onChange={(e) => setScheduleMode(e.target.value)}
          >
            {SCHEDULE_PRESETS.map((preset) => (
              <Radio key={preset.value} value={preset.value}>
                {preset.label}
              </Radio>
            ))}
          </Radio.Group>
        </Form.Item>

        {scheduleMode === "advanced" && (
          <Form.Item
            label="Cron Expression"
            rules={[
              { required: true, message: "Please enter a cron expression" },
            ]}
          >
            <Input
              placeholder="0 * * * *"
              value={customSchedule}
              onChange={(e) => setCustomSchedule(e.target.value)}
              style={{ fontFamily: "monospace" }}
            />
          </Form.Item>
        )}

        {scheduleMode === "advanced" && (
          <Form.Item>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              5-field cron expression (minute hour day month weekday).{" "}
              <a href="https://crontab.guru" target="_blank" rel="noopener noreferrer">
                Learn more
              </a>
            </Typography.Text>
          </Form.Item>
        )}

        <Form.Item style={{ marginBottom: 0 }}>
          <Space>
            <Button type="primary" htmlType="submit" loading={loading}>
              {isEditing ? "Update" : "Create"}
            </Button>
            <Button onClick={handleModalClose}>Cancel</Button>
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  );
};
