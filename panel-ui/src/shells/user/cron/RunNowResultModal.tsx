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
      <Space direction="vertical" style={{ width: "100%" }}>
        <Alert
          message={isSuccess ? "Execution Successful" : "Execution Failed"}
          description={`Exit code: ${result.exit_code}`}
          type={isSuccess ? "success" : "error"}
          showIcon
        />

        {result.stdout && (
          <Card size="small" title="Standard Output">
            <pre
              style={{
                fontFamily: "monospace",
                fontSize: "12px",
                margin: 0,
                maxHeight: "300px",
                overflow: "auto",
                backgroundColor: "#f5f5f5",
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
            size="small"
            title="Standard Error"
            style={{ borderColor: "#ff4d4f" }}
          >
            <pre
              style={{
                fontFamily: "monospace",
                fontSize: "12px",
                margin: 0,
                maxHeight: "300px",
                overflow: "auto",
                backgroundColor: "#fff1f0",
                padding: "12px",
                borderRadius: "4px",
                whiteSpace: "pre-wrap",
                wordWrap: "break-word",
                color: "#ff4d4f",
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
