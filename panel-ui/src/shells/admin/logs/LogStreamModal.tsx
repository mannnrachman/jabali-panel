import { useState, useEffect, useRef } from "react";
import { Modal, Typography, Button, Space, message, Spin } from "antd";
import { CloseOutlined, PauseOutlined, PlayCircleOutlined, ClearOutlined } from "@ant-design/icons";

const { Text } = Typography;

interface LogStreamModalProps {
  visible: boolean;
  onClose: () => void;
  streamUrl: string | null;
  title: string;
  logType: "access" | "error" | "goaccess";
}

export const LogStreamModal = ({ visible, onClose, streamUrl, title, logType }: LogStreamModalProps) => {
  const [logs, setLogs] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const [paused, setPaused] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const logsEndRef = useRef<HTMLDivElement>(null);
  const pausedLogsRef = useRef<string[]>([]);
  const goAccessFrameRef = useRef<HTMLIFrameElement>(null);

  const scrollToBottom = () => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
  };

  useEffect(() => {
    if (visible && streamUrl && !wsRef.current) {
      connectWebSocket();
    }

    return () => {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [visible, streamUrl]);

  useEffect(() => {
    if (!paused) {
      scrollToBottom();
    }
  }, [logs, paused]);

  // For GoAccess: write the latest HTML message into the iframe
  // whenever logs grow. The iframe element is conditionally
  // rendered only when logs.length > 0, so the first message's
  // onmessage handler captures a null ref and can't write —
  // this effect runs AFTER the re-render mounted the iframe and
  // bridges the gap so the dashboard appears on the very first
  // tick instead of waiting 10s for the second.
  useEffect(() => {
    if (logType !== "goaccess" || logs.length === 0) return;
    const frame = goAccessFrameRef.current;
    if (!frame) return;
    const latest = logs[logs.length - 1];
    if (!latest.includes("<html")) return;
    try {
      const doc = frame.contentDocument;
      if (doc) {
        doc.open();
        doc.write(latest);
        doc.close();
      }
    } catch (err) {
      console.warn("Could not update GoAccess frame:", err);
    }
  }, [logs, logType]);

  const connectWebSocket = () => {
    if (!streamUrl) return;

    setConnecting(true);
    const ws = new WebSocket(streamUrl);

    ws.onopen = () => {
      setConnected(true);
      setConnecting(false);
      message.success("Connected to log stream");
      console.log("WebSocket connected");
    };

    ws.onmessage = (event) => {
      if (!paused) {
        const logLine = event.data;
        setLogs(prev => [...prev, logLine]);

        // For GoAccess, update iframe if the message contains HTML
        if (logType === "goaccess" && goAccessFrameRef.current) {
          try {
            const frameDoc = goAccessFrameRef.current.contentDocument;
            if (frameDoc && logLine.includes("<html")) {
              frameDoc.open();
              frameDoc.write(logLine);
              frameDoc.close();
            }
          } catch (error) {
            console.warn("Could not update GoAccess frame:", error);
          }
        }
      } else {
        pausedLogsRef.current.push(event.data);
      }
    };

    ws.onclose = (event) => {
      setConnected(false);
      setConnecting(false);
      if (event.code === 1000) {
        message.info("Log stream ended");
      } else {
        message.error("Connection lost");
      }
      console.log("WebSocket closed", event.code, event.reason);
    };

    ws.onerror = (error) => {
      setConnecting(false);
      message.error("WebSocket connection error");
      console.error("WebSocket error:", error);
    };

    wsRef.current = ws;
  };

  const handleClose = () => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setLogs([]);
    setConnected(false);
    setPaused(false);
    pausedLogsRef.current = [];
    onClose();
  };

  const handlePauseToggle = () => {
    const newPaused = !paused;
    setPaused(newPaused);

    if (!newPaused && pausedLogsRef.current.length > 0) {
      // Resume: add paused logs to display
      setLogs(prev => [...prev, ...pausedLogsRef.current]);
      pausedLogsRef.current = [];
    }
  };

  const handleClearLogs = () => {
    setLogs([]);
    pausedLogsRef.current = [];
  };

  const renderLogContent = () => {
    if (logType === "goaccess") {
      // For GoAccess, use iframe to safely display HTML content
      return (
        <div style={{ height: "500px", border: "1px solid #d9d9d9", borderRadius: "4px" }}>
          {logs.length === 0 ? (
            <div style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              height: "100%",
              backgroundColor: "#fafafa"
            }}>
              <Spin spinning={connecting}>
                <Text type="secondary">
                  {connecting ? "Connecting to GoAccess..." : "Waiting for GoAccess data..."}
                </Text>
              </Spin>
            </div>
          ) : (
            <iframe
              ref={goAccessFrameRef}
              style={{
                width: "100%",
                height: "100%",
                border: "none",
                borderRadius: "4px"
              }}
              title="GoAccess Dashboard"
              sandbox="allow-scripts"
            />
          )}
        </div>
      );
    }

    // For access and error logs, show as text
    return (
      <div
        style={{
          height: "500px",
          overflow: "auto",
          backgroundColor: "#1f1f1f",
          color: "#ffffff",
          fontFamily: "Monaco, Consolas, monospace",
          fontSize: "12px",
          padding: "10px",
          border: "1px solid #d9d9d9",
          borderRadius: "4px"
        }}
      >
        {logs.length === 0 ? (
          <div style={{ textAlign: "center", padding: "20px", color: "#888" }}>
            <Spin spinning={connecting}>
              <div>
                {connecting ? "Connecting to log stream..." : "Waiting for log data..."}
              </div>
            </Spin>
          </div>
        ) : (
          <pre style={{ margin: 0, whiteSpace: "pre-wrap", wordWrap: "break-word" }}>
            {logs.join("\n")}
            <div ref={logsEndRef} />
          </pre>
        )}
      </div>
    );
  };

  return (
    <Modal
      title={title}
      open={visible}
      onCancel={handleClose}
      width={1000}
      footer={null}
      destroyOnClose
    >
      <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        <Space>
          <Button
            type={paused ? "primary" : "default"}
            icon={paused ? <PlayCircleOutlined /> : <PauseOutlined />}
            onClick={handlePauseToggle}
            disabled={!connected}
          >
            {paused ? "Resume" : "Pause"}
          </Button>
          <Button
            icon={<ClearOutlined />}
            onClick={handleClearLogs}
          >
            Clear
          </Button>
          <Button
            danger
            icon={<CloseOutlined />}
            onClick={handleClose}
          >
            Close Stream
          </Button>
          <Text type={connected ? "success" : "secondary"}>
            Status: {connecting ? "Connecting..." : connected ? "Connected" : "Disconnected"}
          </Text>
          {logs.length > 0 && (
            <Text type="secondary">
              {logs.length} lines {paused && pausedLogsRef.current.length > 0 &&
                `(+${pausedLogsRef.current.length} paused)`}
            </Text>
          )}
        </Space>

        {renderLogContent()}
      </Space>
    </Modal>
  );
};