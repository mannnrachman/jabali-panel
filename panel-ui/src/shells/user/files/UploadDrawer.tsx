// UploadDrawer — multi-file upload UX.
//
// Click "Upload" on the FileManagerPage → this drawer slides in from the
// right. The drawer hosts:
//
//   1. AntD Upload.Dragger (drop zone + click-to-pick), multiple=true
//      so the user can queue several files at once.
//   2. A list of every queued file with per-file progress bar and
//      status (queued / uploading N% / done / error). Failed rows
//      keep the error message so the user can read what went wrong
//      without diving into DevTools.
//   3. Footer: total progress + "Close" / "Clear completed" actions.
//
// Concurrency: sequential. Two reasons: (a) the agent's UDS pipe
// serialises calls anyway, so two parallel uploads queue at the agent
// boundary with no real speed-up; (b) it keeps the per-file progress
// bar honest — the active file's bar moves while queued files sit at
// 0%, instead of all bars rising in lockstep.
//
// Path-routing rule: ≤100 MB → single-multipart /files/upload (xhr
// with onUploadProgress); >100 MB → /files/upload-chunk (10 MB chunks,
// resumable). Same split as the old inline implementation.
import {
  CheckCircleFilled,
  CloseCircleFilled,
  CloseOutlined,
  InboxOutlined,
} from "@ant-design/icons";
import { Button, Drawer, List, Progress, Space, Typography, Upload } from "antd";
import type { UploadProps } from "antd";
import { AxiosError } from "axios";
import { forwardRef, useCallback, useImperativeHandle, useMemo, useRef, useState } from "react";
import { filesUpload, filesUploadChunked } from "./filesApi";

export interface UploadDrawerHandle {
  /** Enqueue a file for upload and open the drawer if closed. */
  enqueue: (file: File) => void;
}

type UploadStatus = "queued" | "uploading" | "success" | "error";

interface UploadItem {
  id: string;
  file: File;
  status: UploadStatus;
  progress: number;
  errorMessage?: string;
}

interface UploadDrawerProps {
  open: boolean;
  currentPath: string;
  onClose: () => void;
  onUploaded: () => void;
  onOpenRequest: () => void;
}

const SINGLE_MULTIPART_CEILING = 100 * 1024 * 1024;
const CHUNK_SIZE = 10 * 1024 * 1024;
const HARD_CEILING = 1024 * 1024 * 1024;

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function errMessage(err: unknown): string {
  if (err instanceof AxiosError) {
    const data = err.response?.data as { detail?: string; error?: string } | undefined;
    if (err.response?.status === 507 || data?.error === "quota_exceeded") {
      return "Disk quota exceeded";
    }
    if (data?.error === "disk_full") {
      return "Server disk full";
    }
    if (data?.error === "file_too_large") {
      return "File too large";
    }
    return data?.detail || data?.error || err.message;
  }
  if (err instanceof Error) return err.message;
  return "Unexpected error";
}

export const UploadDrawer = forwardRef<UploadDrawerHandle, UploadDrawerProps>(function UploadDrawer(
  { open, currentPath, onClose, onUploaded, onOpenRequest },
  ref,
) {
  const [items, setItems] = useState<UploadItem[]>([]);
  const [running, setRunning] = useState(false);
  // queue of items waiting to be processed; ref so the worker loop sees
  // the latest set without re-running on every state change
  const queueRef = useRef<UploadItem[]>([]);

  const updateItem = useCallback((id: string, patch: Partial<UploadItem>) => {
    setItems((prev) =>
      prev.map((it) => (it.id === id ? { ...it, ...patch } : it)),
    );
  }, []);

  const runOne = useCallback(
    async (item: UploadItem) => {
      try {
        if (item.file.size > HARD_CEILING) {
          throw new Error("exceeds 1 GB hard limit");
        }
        updateItem(item.id, { status: "uploading", progress: 0 });
        if (item.file.size <= SINGLE_MULTIPART_CEILING) {
          await filesUpload(currentPath, item.file, (frac) => {
            updateItem(item.id, { progress: frac });
          });
        } else {
          await filesUploadChunked(currentPath, item.file, CHUNK_SIZE, (frac) => {
            updateItem(item.id, { progress: frac });
          });
        }
        updateItem(item.id, { status: "success", progress: 1 });
      } catch (err) {
        updateItem(item.id, {
          status: "error",
          errorMessage: errMessage(err),
        });
      }
    },
    [currentPath, updateItem],
  );

  const processQueue = useCallback(async () => {
    if (running) return;
    setRunning(true);
    try {
      // Drain queueRef sequentially. New items added during the run
      // are picked up because we re-read queueRef after each iteration.
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const next = queueRef.current.shift();
        if (!next) break;
        await runOne(next);
      }
      onUploaded();
    } finally {
      setRunning(false);
    }
  }, [running, runOne, onUploaded]);

  const enqueue = useCallback(
    (file: File) => {
      const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const item: UploadItem = {
        id,
        file,
        status: "queued",
        progress: 0,
      };
      setItems((prev) => [...prev, item]);
      queueRef.current.push(item);
      onOpenRequest();
      void processQueue();
    },
    [processQueue, onOpenRequest],
  );

  useImperativeHandle(ref, () => ({ enqueue }), [enqueue]);

  const uploadProps: UploadProps = useMemo(
    () => ({
      name: "file",
      multiple: true,
      showUploadList: false,
      beforeUpload: (file) => {
        enqueue(file);
        return false; // we own the upload
      },
    }),
    [enqueue],
  );

  const totalProgress = useMemo(() => {
    if (items.length === 0) return 0;
    const sum = items.reduce(
      (acc, it) => acc + (it.status === "success" ? 1 : it.progress),
      0,
    );
    return sum / items.length;
  }, [items]);

  const completedCount = items.filter((it) => it.status === "success").length;
  const errorCount = items.filter((it) => it.status === "error").length;

  const clearCompleted = () => {
    setItems((prev) =>
      prev.filter((it) => it.status !== "success" && it.status !== "error"),
    );
  };

  return (
    <Drawer
      title="Upload files"
      open={open}
      onClose={onClose}
      width={560}
      destroyOnHidden={false}
      extra={
        <Button type="text" icon={<CloseOutlined />} onClick={onClose} />
      }
      footer={
        items.length > 0 ? (
          <Space style={{ width: "100%", justifyContent: "space-between" }}>
            <Typography.Text type="secondary">
              {completedCount}/{items.length} done
              {errorCount > 0 ? ` · ${errorCount} failed` : ""}
            </Typography.Text>
            <Space>
              {(completedCount > 0 || errorCount > 0) && (
                <Button onClick={clearCompleted} disabled={running}>
                  Clear completed
                </Button>
              )}
              <Button onClick={onClose}>Close</Button>
            </Space>
          </Space>
        ) : null
      }
    >
      <Space direction="vertical" size="middle" style={{ width: "100%" }}>
        <Upload.Dragger {...uploadProps}>
          <p className="ant-upload-drag-icon">
            <InboxOutlined />
          </p>
          <p className="ant-upload-text">
            Click or drag files here to upload
          </p>
          <p className="ant-upload-hint">
            Multiple files supported. Files &gt; 100 MB use chunked upload
            with resume on disconnect.
          </p>
        </Upload.Dragger>

        {items.length > 0 && (
          <Progress
            percent={Math.round(totalProgress * 100)}
            status={
              errorCount > 0 && !running
                ? "exception"
                : running
                  ? "active"
                  : "success"
            }
          />
        )}

        <List
          dataSource={items}
          locale={{ emptyText: " " }}
          renderItem={(it) => (
            <List.Item key={it.id}>
              <Space direction="vertical" size={4} style={{ width: "100%" }}>
                <Space style={{ width: "100%", justifyContent: "space-between" }}>
                  <Typography.Text strong ellipsis style={{ maxWidth: 360 }}>
                    {it.file.name}
                  </Typography.Text>
                  <Typography.Text type="secondary">
                    {formatBytes(it.file.size)}
                  </Typography.Text>
                </Space>
                {it.status === "queued" && (
                  <Typography.Text type="secondary">Queued</Typography.Text>
                )}
                {it.status === "uploading" && (
                  <Progress
                    percent={Math.round(it.progress * 100)}
                    size="small"
                    status="active"
                  />
                )}
                {it.status === "success" && (
                  <Typography.Text type="success">
                    <CheckCircleFilled /> Uploaded
                  </Typography.Text>
                )}
                {it.status === "error" && (
                  <Typography.Text type="danger">
                    <CloseCircleFilled /> {it.errorMessage}
                  </Typography.Text>
                )}
              </Space>
            </List.Item>
          )}
        />
      </Space>
    </Drawer>
  );
});
