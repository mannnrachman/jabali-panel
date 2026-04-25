// JobLogTail — pre-formatted log tail with auto-scroll. Used by both the
// Jabali update card and the apt-upgrade card; pass the live `log_tail`
// string and the unit status. Empty tail renders nothing.
import { useEffect, useRef } from "react";
import { Tag } from "antd";

interface Props {
  status: string;
  logTail: string;
  exitCode?: number;
}

const STATUS_COLOR: Record<string, string> = {
  active: "blue",
  activating: "blue",
  inactive: "default",
  failed: "red",
};

export function JobLogTail({ status, logTail, exitCode }: Props) {
  const ref = useRef<HTMLPreElement | null>(null);
  useEffect(() => {
    if (ref.current) {
      ref.current.scrollTop = ref.current.scrollHeight;
    }
  }, [logTail]);

  if (!logTail) return null;

  return (
    <div style={{ marginTop: 12 }}>
      <div style={{ marginBottom: 8 }}>
        <Tag color={STATUS_COLOR[status] ?? "default"}>{status}</Tag>
        {exitCode !== undefined && (
          <Tag color={exitCode === 0 ? "green" : "red"}>exit {exitCode}</Tag>
        )}
      </div>
      <pre
        ref={ref}
        style={{
          background: "#0a0a0a",
          color: "#d4d4d4",
          padding: 12,
          borderRadius: 6,
          maxHeight: 320,
          overflow: "auto",
          fontFamily: "ui-monospace, SFMono-Regular, monospace",
          fontSize: 12,
          lineHeight: 1.45,
          margin: 0,
          whiteSpace: "pre-wrap",
          wordBreak: "break-word",
        }}
      >
        {logTail}
      </pre>
    </div>
  );
}
