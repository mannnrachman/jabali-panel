// DiagnosticReportModal — runs the two-step diagnostic flow.
//
// Step 1 (auto): POST /admin/support/diagnostic uploads the redacted +
// encrypted bundle to enclosed.jabali-panel.com. Modal shows the link
// + password with copy buttons + an instruction to send via ntfy.
//
// Step 2 (operator click): POST /admin/support/diagnostic/notify pushes
// {hostname, link, password} to ntfy.jabali-panel.com/jabali-admin-alerts
// — the team gets a mobile notification with everything they need.
import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Input,
  Modal,
  Space,
  Spin,
  Tag,
  Typography,
  message,
} from "antd";

import { CopyOutlined, ExportOutlined, SendOutlined } from "@icons";

import {
  useDiagnosticNotify,
  useDiagnosticReport,
} from "../../../hooks/useSupport";

interface Props {
  open: boolean;
  onClose: () => void;
}

export function DiagnosticReportModal({ open, onClose }: Props) {
  const report = useDiagnosticReport();
  const notify = useDiagnosticNotify();
  const [note, setNote] = useState("");

  // Fire on first open. Reset state on close so a second open redoes
  // everything from scratch.
  useEffect(() => {
    if (open && !report.isPending && !report.data && !report.error) {
      report.mutate();
    }
    if (!open) {
      report.reset();
      notify.reset();
      setNote("");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      message.success(`${label} copied`);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "copy failed");
    }
  };

  const onSendNtfy = async () => {
    if (!report.data) return;
    try {
      await notify.mutateAsync({
        url: report.data.url,
        password: report.data.password,
        note: note.trim() || undefined,
      });
      message.success("Team notified via ntfy");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "notify failed");
    }
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title="Diagnostic Report"
      width={780}
      footer={[
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
      ]}
    >
      {report.isPending ? (
        <div style={{ padding: 32, textAlign: "center" }}>
          <Spin tip="Collecting host state, redacting secrets, uploading…" />
        </div>
      ) : null}

      {report.error ? (
        <Alert
          type="error"
          showIcon
          message="Failed to generate report"
          description={(report.error as Error).message}
        />
      ) : null}

      {report.data ? (
        <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            message="Encrypted bundle uploaded"
            description={
              <Space size={4} wrap>
                <Tag>{report.data.file_count} files</Tag>
                <Tag color="blue">{report.data.redaction_count} redactions</Tag>
                <Tag>{report.data.byte_count.toLocaleString()} bytes</Tag>
                <Tag color="default">7-day TTL</Tag>
              </Space>
            }
          />

          <div>
            <Typography.Text strong>Link</Typography.Text>
            <Space.Compact style={{ display: "flex", marginTop: 4 }}>
              <Input value={report.data.url} readOnly />
              <Button
                icon={<CopyOutlined />}
                onClick={() => copy("Link", report.data!.url)}
              >
                Copy
              </Button>
              <Button
                icon={<ExportOutlined />}
                href={report.data.url}
                target="_blank"
                rel="noopener noreferrer"
              >
                Open
              </Button>
            </Space.Compact>
          </div>

          <div>
            <Typography.Text strong>Password</Typography.Text>
            <Space.Compact style={{ display: "flex", marginTop: 4 }}>
              <Input value={report.data.password} readOnly />
              <Button
                icon={<CopyOutlined />}
                onClick={() => copy("Password", report.data!.password)}
              >
                Copy
              </Button>
            </Space.Compact>
          </div>

          <Alert
            type="info"
            showIcon
            message="Send the link to the Jabali team"
            description={
              <Space direction="vertical" size={8} style={{ width: "100%" }}>
                <Typography.Paragraph style={{ margin: 0 }}>
                  Click <b>Send via ntfy</b> below to push this link + password
                  to the team's alert channel{" "}
                  <Typography.Text code>{report.data.ntfy_topic}</Typography.Text>.
                  They'll see it on their mobile clients within seconds.
                </Typography.Paragraph>
                <Input.TextArea
                  rows={2}
                  placeholder="Optional context (what's broken, what you've tried)"
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  disabled={notify.isPending || notify.isSuccess}
                />
                <Space>
                  <Button
                    type="primary"
                    icon={<SendOutlined />}
                    loading={notify.isPending}
                    disabled={notify.isSuccess}
                    onClick={onSendNtfy}
                  >
                    {notify.isSuccess ? "Sent ✓" : "Send via ntfy"}
                  </Button>
                  {notify.isSuccess ? (
                    <Typography.Text type="success">
                      Team notified.
                    </Typography.Text>
                  ) : null}
                </Space>
              </Space>
            }
          />
        </Space>
      ) : null}
    </Modal>
  );
}
