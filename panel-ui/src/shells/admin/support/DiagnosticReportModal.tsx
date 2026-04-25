// DiagnosticReportModal — fires the diagnostic-report mutation, shows
// the resulting age ciphertext in a copyable textarea, and tells the
// operator how to use it.
import { useEffect } from "react";
import { Alert, Button, Modal, Space, Spin, Tag, Typography, message } from "antd";

import { CopyOutlined } from "@icons";

import { useDiagnosticReport } from "../../../hooks/useSupport";

interface Props {
  open: boolean;
  onClose: () => void;
}

export function DiagnosticReportModal({ open, onClose }: Props) {
  const mutation = useDiagnosticReport();

  // Fire on open. Reset on close so a second open redoes the call.
  useEffect(() => {
    if (open && !mutation.isPending && !mutation.data && !mutation.error) {
      mutation.mutate();
    }
    if (!open) {
      mutation.reset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const onCopy = async () => {
    if (!mutation.data) return;
    try {
      await navigator.clipboard.writeText(mutation.data.ciphertext_b64);
      message.success("Copied to clipboard");
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : "copy failed");
    }
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title="Diagnostic Report"
      width={760}
      footer={[
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
        <Button
          key="copy"
          type="primary"
          icon={<CopyOutlined />}
          disabled={!mutation.data}
          onClick={onCopy}
        >
          Copy to clipboard
        </Button>,
      ]}
    >
      {mutation.isPending ? (
        <div style={{ padding: 32, textAlign: "center" }}>
          <Spin tip="Collecting host state, redacting secrets, encrypting…" />
        </div>
      ) : null}

      {mutation.error ? (
        <Alert
          type="error"
          showIcon
          message="Failed to generate report"
          description={(mutation.error as Error).message}
        />
      ) : null}

      {mutation.data ? (
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Alert
            type="success"
            showIcon
            message="Report ready"
            description={
              <Space size={4} wrap>
                <Tag>{mutation.data.file_count} files</Tag>
                <Tag color="blue">{mutation.data.redaction_count} redactions</Tag>
                <Tag color="default">{mutation.data.byte_count.toLocaleString()} bytes</Tag>
              </Space>
            }
          />
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            Paste this ciphertext in your GitHub issue. It is age-encrypted to
            the Jabali team's static recipient — only the team can decrypt.
            Sensitive strings (passwords, session tokens, Bearer headers) were
            redacted before encryption.
          </Typography.Paragraph>
          <Typography.Text
            code
            copyable={{ text: mutation.data.ciphertext_b64 }}
            style={{
              display: "block",
              maxHeight: 320,
              overflow: "auto",
              padding: 12,
              background: "#0a0a0a",
              color: "#d4d4d4",
              borderRadius: 6,
              fontSize: 11,
              wordBreak: "break-all",
              whiteSpace: "pre-wrap",
            }}
          >
            {mutation.data.ciphertext_b64}
          </Typography.Text>
        </Space>
      ) : null}
    </Modal>
  );
}
