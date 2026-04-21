import { Modal, Alert, Card, Space } from "antd";

interface RunNowResultModalProps {
  open: boolean;
  onClose: () => void;
  result: {
    exit_code: number;
    stdout: string;
    stderr: string;
  } | null;
}

export const RunNowResultModal = ({
  open,
  onClose,
  result,
}: RunNowResultModalProps) => {
  if (!result) {
    return null;
  }

  const isSuccess = result.exit_code === 0;

  return (
    <Modal
      title="Cron Job Execution Result"
      open={open}
      onCancel={onClose}
      footer={null}
      width={700}
    >
      <Space orientation="vertical" style={{ width: "100%" }}>
        <Alert
          title={isSuccess ? "Execution Successful" : "Execution Failed"}
          description={`Exit code: ${result.exit_code}`}
          type={isSuccess ? "success" : "error"}
          showIcon
        />

        {result.stdout && (
          <Card title="Standard Output">
            <pre
              style={{
                fontFamily: "monospace",
                margin: 0,
                maxHeight: "300px",
                overflow: "auto",
                backgroundColor: "var(--ant-color-bg-container)",
                padding: "12px",
                borderRadius: "4px",
                whiteSpace: "pre-wrap",
                wordWrap: "break-word",
              }}
            >
              {result.stdout}
            </pre>
          </Card>
        )}

        {result.stderr && (
          <Card
            title="Standard Error"
            style={{ borderColor: "var(--ant-color-error)" }}
          >
            <pre
              style={{
                fontFamily: "monospace",
                margin: 0,
                maxHeight: "300px",
                overflow: "auto",
                backgroundColor: "var(--ant-color-error-bg)",
                padding: "12px",
                borderRadius: "4px",
                whiteSpace: "pre-wrap",
                wordWrap: "break-word",
                color: "var(--ant-color-error)",
              }}
            >
              {result.stderr}
            </pre>
          </Card>
        )}
      </Space>
    </Modal>
  );
};
