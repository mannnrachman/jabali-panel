// FileManagerPage — AntD-native file manager for /jabali-panel/files (M11).
//
// Layout:
//   Breadcrumb + action buttons (top)
//   Tree (left, lazy-loaded dirs)  |  Table (right, entries of current dir)
//
// Primary ops per row: download, preview (text, ≤1 MiB), rename, delete.
// Toolbar: upload file, new folder, refresh.
//
// Drag-and-drop (added post-Phase-1):
//   - Drop OS files on the table to upload to currentPath (AntD Dragger).
//   - Drag a row onto a folder row (or onto a tree node) to move the file
//     there. Cross-directory move goes through /files/move, which is
//     distinct from rename (same-parent only).
//
// Scope: still no multi-select, no chmod, no image preview, no editor —
// those remain Phase 2.
import type { ReactNode } from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Breadcrumb,
  Button,
  Dropdown,
  Empty,
  Input,
  Modal,
  Space,
  Spin,
  Table,
  Tree,
  Typography,
  Upload,
  message,
} from "antd";
import type { UploadProps } from "antd";
import type { DataNode } from "antd/es/tree";
import {
  DownloadOutlined,
  EditOutlined,
  EyeOutlined,
  FileOutlined,
  FolderOutlined,
  MoreOutlined,
  PlusOutlined,
  ReloadOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import { AxiosError } from "axios";

import type { FileEntry } from "./filesApi";
import {
  filesChmod,
  filesDelete,
  filesDownloadURL,
  filesHome,
  filesList,
  filesMkdir,
  filesMove,
  filesPreview,
  filesRename,
  filesTree,
  filesUpload,
} from "./filesApi";

const { Text } = Typography;

// Custom DataTransfer MIME for row drags, so the parent OS-file drop
// handler can distinguish a "dragging a row around inside the table"
// event from a "dragging a file in from Finder" event. The payload is
// always a JSON-encoded string[] of absolute paths — single-row drags
// are a one-element array, multi-select drags send all selected rows.
const dragPathMime = "application/x-jabali-file-path";

// parseDragPayload accepts both the new array-JSON form and the legacy
// single-path form (pre-bulk-selection) so a row in flight at the
// moment of deploy doesn't land as a silent no-op.
function parseDragPayload(raw: string): string[] {
  try {
    const v = JSON.parse(raw);
    if (Array.isArray(v) && v.every((x) => typeof x === "string")) {
      return v;
    }
  } catch {
    // not JSON — fall through to treat as single path
  }
  return raw ? [raw] : [];
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let size = bytes;
  let i = 0;
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024;
    i++;
  }
  return i === 0 ? `${Math.floor(size)} B` : `${size.toFixed(1)} ${units[i]}`;
}

function formatModTime(raw: string): string {
  // Agent emits Go's default time.String() format, e.g.
  //   "2026-04-18 19:41:23.123456789 +0000 UTC"
  // Trim everything after seconds for display.
  return raw.slice(0, 19);
}

function joinPath(dir: string, name: string): string {
  return dir.endsWith("/") ? dir + name : dir + "/" + name;
}

// Folders-first, then alphabetical (case-insensitive, locale-aware).
// Matches every desktop file manager's default — GNOME Files, Finder, Explorer —
// and keeps dotfiles/dotdirs naturally sorted within their group.
function sortEntries(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  });
}

function errMessage(err: unknown): string {
  if (err instanceof AxiosError) {
    const data = err.response?.data as { detail?: string; error?: string } | undefined;
    return data?.detail || data?.error || err.message;
  }
  if (err instanceof Error) return err.message;
  return "Unexpected error";
}

type TreeNode = DataNode & { path: string };

function makeTreeNode(path: string, name: string): TreeNode {
  return {
    key: path,
    title: name,
    path,
    isLeaf: false,
  };
}

export const FileManagerPage = () => {
  const [rootPath, setRootPath] = useState<string | null>(null);
  const [currentPath, setCurrentPath] = useState<string | null>(null);
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [listLoading, setListLoading] = useState(false);
  const [treeData, setTreeData] = useState<TreeNode[]>([]);
  const [expandedKeys, setExpandedKeys] = useState<string[]>([]);

  const [mkdirOpen, setMkdirOpen] = useState(false);
  const [mkdirName, setMkdirName] = useState("");
  const mkdirSubmitting = useRef(false);

  const [renameOpen, setRenameOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState<FileEntry | null>(null);
  const [renameNewName, setRenameNewName] = useState("");
  const renameSubmitting = useRef(false);

  const [previewOpen, setPreviewOpen] = useState(false);
  const [previewPath, setPreviewPath] = useState<string | null>(null);
  const [previewContent, setPreviewContent] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);

  // --- bulk selection ---
  // selectedNames holds the row keys (entry.name) of checked rows in the
  // CURRENT dir. Cleared whenever currentPath changes — mixing selections
  // across directories would be surprising and makes the "Delete N" button
  // lie about what it'll actually delete.
  const [selectedNames, setSelectedNames] = useState<string[]>([]);

  const [bulkMoveOpen, setBulkMoveOpen] = useState(false);
  const [bulkMoveDest, setBulkMoveDest] = useState("");
  const [bulkChmodOpen, setBulkChmodOpen] = useState(false);
  const [bulkChmodMode, setBulkChmodMode] = useState("0644");

  // --- initial load: fetch user's home then list it ---
  useEffect(() => {
    (async () => {
      try {
        const home = await filesHome();
        setRootPath(home.path);
        setCurrentPath(home.path);
        setTreeData([makeTreeNode(home.path, home.path)]);
        setExpandedKeys([home.path]);
      } catch (err) {
        message.error(`Cannot open file manager: ${errMessage(err)}`);
      }
    })();
  }, []);

  // --- list current dir ---
  const reloadList = useCallback(async (path: string) => {
    setListLoading(true);
    try {
      const resp = await filesList(path);
      setEntries(sortEntries(resp.entries));
    } catch (err) {
      message.error(`List failed: ${errMessage(err)}`);
      setEntries([]);
    } finally {
      setListLoading(false);
    }
  }, []);

  useEffect(() => {
    if (currentPath) void reloadList(currentPath);
    // Moving between folders wipes the selection — holding it would mean
    // a "Delete 3 items" button that referenced rows you could no longer see.
    setSelectedNames([]);
  }, [currentPath, reloadList]);

  // Esc clears the selection — matches every desktop file manager.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setSelectedNames([]);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // --- tree: lazy-load children on expand ---
  const loadTreeChildren = useCallback(async (node: TreeNode): Promise<void> => {
    try {
      const resp = await filesTree(node.path);
      const children: TreeNode[] = resp.entries.map((e) => ({
        key: joinPath(node.path, e.name),
        title: e.name,
        path: joinPath(node.path, e.name),
        isLeaf: false,
      }));
      setTreeData((prev) => updateTreeNode(prev, node.path, children));
    } catch (err) {
      message.error(`Tree load failed: ${errMessage(err)}`);
    }
  }, []);

  // --- actions ---
  const handleRefresh = () => {
    if (currentPath) void reloadList(currentPath);
    // Also refresh the expanded tree nodes up to current so new dirs show.
    if (rootPath) void loadTreeChildren({ key: rootPath, title: rootPath, path: rootPath });
  };

  const handleRowClick = (entry: FileEntry) => {
    if (!currentPath) return;
    if (entry.is_dir) {
      const next = joinPath(currentPath, entry.name);
      setCurrentPath(next);
      // Expand the parent (currentPath) and lazy-load its children so the
      // new child appears in the tree on the left. Without this, drilling
      // down via the table leaves the tree stuck at the last node the user
      // expanded manually via the chevron.
      setExpandedKeys((prev) => {
        const s = new Set(prev);
        s.add(currentPath);
        s.add(next);
        return Array.from(s);
      });
      void loadTreeChildren({ key: currentPath, title: currentPath, path: currentPath });
    }
  };

  const handlePreview = async (entry: FileEntry) => {
    if (!currentPath) return;
    const path = joinPath(currentPath, entry.name);
    setPreviewPath(path);
    setPreviewContent("");
    setPreviewOpen(true);
    setPreviewLoading(true);
    try {
      const resp = await filesPreview(path);
      setPreviewContent(resp.content);
    } catch (err) {
      message.error(`Preview failed: ${errMessage(err)}`);
      setPreviewOpen(false);
    } finally {
      setPreviewLoading(false);
    }
  };

  const handleDownload = (entry: FileEntry) => {
    if (!currentPath) return;
    const path = joinPath(currentPath, entry.name);
    // Open in a new tab; the browser will save as an attachment due to
    // our server-side Content-Disposition header.
    window.open(filesDownloadURL(path), "_blank", "noopener,noreferrer");
  };

  const handleDelete = async (entry: FileEntry) => {
    if (!currentPath) return;
    const path = joinPath(currentPath, entry.name);
    try {
      await filesDelete(path, entry.is_dir);
      message.success(`Deleted ${entry.name}`);
      void reloadList(currentPath);
    } catch (err) {
      message.error(`Delete failed: ${errMessage(err)}`);
    }
  };

  const confirmDelete = (entry: FileEntry) => {
    Modal.confirm({
      title: `Delete "${entry.name}"?`,
      content: entry.is_dir ? "Folder and everything inside will be removed." : undefined,
      okText: "Delete",
      okButtonProps: { danger: true },
      onOk: () => handleDelete(entry),
    });
  };

  const openRename = (entry: FileEntry) => {
    setRenameTarget(entry);
    setRenameNewName(entry.name);
    setRenameOpen(true);
  };

  const submitRename = async () => {
    if (!currentPath || !renameTarget || renameSubmitting.current) return;
    const newName = renameNewName.trim();
    if (!newName || newName.includes("/") || newName === "." || newName === "..") {
      message.error("Invalid new name");
      return;
    }
    renameSubmitting.current = true;
    try {
      const path = joinPath(currentPath, renameTarget.name);
      await filesRename(path, newName);
      message.success(`Renamed to ${newName}`);
      setRenameOpen(false);
      void reloadList(currentPath);
    } catch (err) {
      message.error(`Rename failed: ${errMessage(err)}`);
    } finally {
      renameSubmitting.current = false;
    }
  };

  const openMkdir = () => {
    setMkdirName("");
    setMkdirOpen(true);
  };

  const submitMkdir = async () => {
    if (!currentPath || mkdirSubmitting.current) return;
    const name = mkdirName.trim();
    if (!name || name.includes("/") || name === "." || name === "..") {
      message.error("Invalid folder name");
      return;
    }
    mkdirSubmitting.current = true;
    try {
      await filesMkdir(joinPath(currentPath, name));
      message.success(`Created ${name}`);
      setMkdirOpen(false);
      void reloadList(currentPath);
    } catch (err) {
      message.error(`Create folder failed: ${errMessage(err)}`);
    } finally {
      mkdirSubmitting.current = false;
    }
  };

  // --- upload via AntD Upload ---
  // Shared by the toolbar button AND the table-wide drop zone: both paths
  // call the same `filesUpload` with a 100 MB cap and a reload on success.
  // `multiple: true` lets the drop zone handle multi-file drops naturally;
  // AntD invokes beforeUpload once per file.
  const uploadProps: UploadProps = useMemo(
    () => ({
      multiple: true,
      showUploadList: false,
      beforeUpload: (file) => {
        if (!currentPath) return false;
        if (file.size > 100 * 1024 * 1024) {
          message.error(`${file.name}: exceeds 100 MB limit`);
          return false;
        }
        (async () => {
          try {
            await filesUpload(currentPath, file);
            message.success(`Uploaded ${file.name}`);
            void reloadList(currentPath);
          } catch (err) {
            message.error(`Upload failed (${file.name}): ${errMessage(err)}`);
          }
        })();
        return false; // prevent AntD's default XHR; we already uploaded.
      },
    }),
    [currentPath, reloadList],
  );

  // --- drag-to-move state ---
  // draggedPath is set on dragstart from a table row; consumed on drop
  // onto a folder row (or tree node) and cleared on dragend.
  const [draggedPath, setDraggedPath] = useState<string | null>(null);

  // --- bulk ops ---
  // Each "bulk X" handler fans out into parallel API calls via
  // Promise.allSettled so a single failure doesn't cancel the rest.
  // The toast summarises N succeeded / M failed; detailed per-item errors
  // still land in the browser console for now. Good enough for v1.

  const selectedPaths = useMemo(() => {
    if (!currentPath) return [] as string[];
    return selectedNames.map((n) => joinPath(currentPath, n));
  }, [currentPath, selectedNames]);

  const selectedEntries = useMemo(
    () => entries.filter((e) => selectedNames.includes(e.name)),
    [entries, selectedNames],
  );

  const runBulk = useCallback(
    async (
      verb: string,
      paths: string[],
      op: (p: string) => Promise<void>,
    ) => {
      const results = await Promise.allSettled(paths.map(op));
      const ok = results.filter((r) => r.status === "fulfilled").length;
      const fail = results.length - ok;
      if (fail === 0) {
        message.success(`${verb} ${ok} item${ok === 1 ? "" : "s"}`);
      } else if (ok === 0) {
        message.error(`${verb} failed for all ${fail} items`);
      } else {
        message.warning(`${verb} ${ok}/${results.length} — ${fail} failed`);
      }
      // Log failures for debug — the toast can't surface per-item detail.
      results.forEach((r, i) => {
        if (r.status === "rejected") {
          console.warn(`[files bulk] ${verb} ${paths[i]} failed:`, r.reason);
        }
      });
      setSelectedNames([]);
      if (currentPath) void reloadList(currentPath);
    },
    [currentPath, reloadList],
  );

  const handleBulkDelete = useCallback(() => {
    if (selectedPaths.length === 0) return;
    const anyDir = selectedEntries.some((e) => e.is_dir);
    Modal.confirm({
      title: `Delete ${selectedPaths.length} item${selectedPaths.length === 1 ? "" : "s"}?`,
      content: anyDir
        ? "Folders will be removed with everything inside them. This cannot be undone."
        : "This cannot be undone.",
      okType: "danger",
      okText: "Delete",
      onOk: () =>
        runBulk("Deleted", selectedPaths, (p) => {
          const entry = selectedEntries.find(
            (e) => joinPath(currentPath || "", e.name) === p,
          );
          return filesDelete(p, entry?.is_dir ?? false);
        }),
    });
  }, [currentPath, runBulk, selectedEntries, selectedPaths]);

  const handleBulkMove = useCallback(async () => {
    const dest = bulkMoveDest.trim();
    if (!dest || !dest.startsWith("/")) {
      message.error("Destination must be an absolute path starting with /");
      return;
    }
    setBulkMoveOpen(false);
    await runBulk("Moved", selectedPaths, (p) => filesMove(p, dest));
    setBulkMoveDest("");
  }, [bulkMoveDest, runBulk, selectedPaths]);

  const handleBulkChmod = useCallback(async () => {
    const mode = bulkChmodMode.trim();
    if (!/^[0-7]{3,4}$/.test(mode)) {
      message.error("Mode must be an octal string like 644 or 0755");
      return;
    }
    setBulkChmodOpen(false);
    await runBulk("Chmod", selectedPaths, (p) => filesChmod(p, mode));
  }, [bulkChmodMode, runBulk, selectedPaths]);

  const handleMove = useCallback(
    async (srcPath: string, destDir: string) => {
      // Refuse a no-op upfront — destDir === dirname(srcPath) means the
      // user dropped onto the same folder the row already lives in. The
      // backend would refuse with "source and destination are the same",
      // but failing silently here keeps the UI calm.
      const lastSlash = srcPath.lastIndexOf("/");
      const srcParent = lastSlash > 0 ? srcPath.slice(0, lastSlash) : "/";
      if (srcParent === destDir) return;
      try {
        await filesMove(srcPath, destDir);
        message.success("Moved");
        if (currentPath) void reloadList(currentPath);
      } catch (err) {
        message.error(`Move failed: ${errMessage(err)}`);
      }
    },
    [currentPath, reloadList],
  );

  // --- breadcrumb segments ---
  const crumbs = useMemo(() => {
    if (!currentPath || !rootPath) return [];
    // Clamp displayed breadcrumbs to rootPath so user can't navigate above home.
    const rel = currentPath.startsWith(rootPath) ? currentPath.slice(rootPath.length) : "";
    const parts = rel.split("/").filter(Boolean);
    const items: { title: ReactNode; path: string }[] = [
      { title: <FolderOutlined />, path: rootPath },
    ];
    let acc = rootPath;
    for (const part of parts) {
      acc = joinPath(acc, part);
      items.push({ title: part, path: acc });
    }
    return items;
  }, [currentPath, rootPath]);

  // --- table columns ---
  const columns = [
    {
      title: "Name",
      dataIndex: "name",
      key: "name",
      render: (_: string, entry: FileEntry) => (
        <Space
          size={8}
          style={{ cursor: entry.is_dir ? "pointer" : "default" }}
          onClick={() => entry.is_dir && handleRowClick(entry)}
        >
          {entry.is_dir ? <FolderOutlined /> : <FileOutlined />}
          <Text>{entry.name}</Text>
          {entry.is_symlink && <Text type="secondary">(link)</Text>}
        </Space>
      ),
    },
    {
      title: "Size",
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (_: number, entry: FileEntry) => (entry.is_dir ? "—" : formatBytes(entry.size)),
    },
    {
      title: "Modified",
      dataIndex: "mod_time",
      key: "mod_time",
      width: 180,
      render: (v: string) => formatModTime(v),
    },
    {
      title: "",
      key: "actions",
      width: 60,
      render: (_: unknown, entry: FileEntry) => {
        const items = [
          ...(!entry.is_dir
            ? [
                {
                  key: "download",
                  icon: <DownloadOutlined />,
                  label: "Download",
                  onClick: () => handleDownload(entry),
                },
                {
                  key: "preview",
                  icon: <EyeOutlined />,
                  label: "Preview",
                  onClick: () => void handlePreview(entry),
                },
              ]
            : []),
          {
            key: "rename",
            icon: <EditOutlined />,
            label: "Rename",
            onClick: () => openRename(entry),
          },
          {
            key: "delete",
            danger: true,
            label: "Delete",
            onClick: () => confirmDelete(entry),
          },
        ];
        return (
          <Dropdown trigger={["click"]} menu={{ items }}>
            <Button type="text" icon={<MoreOutlined />} />
          </Dropdown>
        );
      },
    },
  ];

  // --- render ---
  if (!rootPath || !currentPath) {
    return (
      <div style={{ display: "flex", justifyContent: "center", padding: 80 }}>
        <Spin size="large" />
      </div>
    );
  }

  return (
    <div style={{ padding: 16 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: 12,
          gap: 16,
          flexWrap: "wrap",
        }}
      >
        <Breadcrumb
          items={crumbs.map((c) => ({
            title: (
              <a
                onClick={(e) => {
                  e.preventDefault();
                  setCurrentPath(c.path);
                }}
              >
                {c.title}
              </a>
            ),
          }))}
        />
        <Space>
          <Upload {...uploadProps}>
            <Button icon={<UploadOutlined />}>Upload</Button>
          </Upload>
          <Button icon={<PlusOutlined />} onClick={openMkdir}>
            New Folder
          </Button>
          <Button icon={<ReloadOutlined />} onClick={handleRefresh} />
        </Space>
      </div>

      {/*
        Bulk-action bar: only rendered when at least one row is checked.
        Sits above the table so the N-count stays visible while the user
        confirms the action in a modal. Clear button mirrors the Esc
        shortcut for mouse users.
      */}
      {selectedNames.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 12,
            marginBottom: 12,
            padding: "8px 12px",
            background: "#f0f5ff",
            border: "1px solid #adc6ff",
            borderRadius: 4,
            // Sticky so bulk actions stay reachable even when scrolled
            // deep into a long folder. top: 0 pins to the viewport top;
            // zIndex keeps it above table rows that may have the row
            // selection highlight. The page itself scrolls on the body,
            // so `sticky` binds to the body scroll container.
            position: "sticky",
            top: 0,
            zIndex: 10,
          }}
        >
          <Text strong>
            {selectedNames.length} selected
          </Text>
          <Button danger size="small" onClick={handleBulkDelete}>
            Delete
          </Button>
          <Button size="small" onClick={() => setBulkMoveOpen(true)}>
            Move to…
          </Button>
          <Button size="small" onClick={() => setBulkChmodOpen(true)}>
            Permissions
          </Button>
          <Button size="small" type="text" onClick={() => setSelectedNames([])}>
            Clear
          </Button>
        </div>
      )}

      <div style={{ display: "flex", gap: 16, alignItems: "stretch" }}>
        <div
          style={{
            width: 280,
            flexShrink: 0,
            border: "1px solid #f0f0f0",
            borderRadius: 4,
            padding: 8,
            maxHeight: "calc(100vh - 200px)",
            overflow: "auto",
          }}
        >
          <Tree
            treeData={treeData}
            expandedKeys={expandedKeys}
            onExpand={(keys) => setExpandedKeys(keys as string[])}
            selectedKeys={[currentPath]}
            loadData={(node) => loadTreeChildren(node as TreeNode)}
            onSelect={(keys) => {
              if (keys.length > 0) setCurrentPath(keys[0] as string);
            }}
            // Tree nodes accept drops of table rows (move into this folder).
            // Hits the same handleMove path as the table row-on-row drop.
            // We attach handlers via `titleRender` so every node in the tree
            // becomes a drop target without opting-in AntD's own tree DnD
            // (which is for reordering nodes — not what we want).
            titleRender={(node) => {
              const treeNode = node as TreeNode;
              return (
                <span
                  onDragOver={(e) => {
                    if (!e.dataTransfer.types.includes(dragPathMime)) return;
                    e.preventDefault();
                    e.dataTransfer.dropEffect = "move";
                  }}
                  onDrop={(e) => {
                    const raw = e.dataTransfer.getData(dragPathMime);
                    if (!raw) return;
                    e.preventDefault();
                    e.stopPropagation();
                    const paths = parseDragPayload(raw);
                    if (paths.length === 1) {
                      void handleMove(paths[0], treeNode.path);
                    } else if (paths.length > 1) {
                      void runBulk("Moved", paths, (p) => filesMove(p, treeNode.path));
                    }
                  }}
                >
                  {treeNode.title as ReactNode}
                </span>
              );
            }}
          />
        </div>

        <div
          style={{ flex: 1, minWidth: 0 }}
          // OS-file drop zone: dragging files in from the desktop/Finder
          // uploads them to the current folder. Custom DataTransfer types
          // (from row drags) are filtered out so we only preventDefault on
          // real OS-file drags — otherwise AntD Table's internal row drag
          // would be swallowed by this parent handler.
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes("Files")) e.preventDefault();
          }}
          onDrop={(e) => {
            if (!e.dataTransfer.types.includes("Files")) return;
            e.preventDefault();
            if (!currentPath) return;
            for (const f of Array.from(e.dataTransfer.files)) {
              if (f.size > 100 * 1024 * 1024) {
                message.error(`${f.name}: exceeds 100 MB limit`);
                continue;
              }
              (async () => {
                try {
                  await filesUpload(currentPath, f);
                  message.success(`Uploaded ${f.name}`);
                  void reloadList(currentPath);
                } catch (err) {
                  message.error(`Upload failed (${f.name}): ${errMessage(err)}`);
                }
              })();
            }
          }}
        >
          <Spin spinning={listLoading}>
            <Table<FileEntry>
              rowKey="name"
              dataSource={entries}
              columns={columns as never}
              pagination={false}
              size="small"
              locale={{ emptyText: <Empty description="Empty directory" /> }}
              // Row drag-to-move: any row is draggable; folders are drop
              // targets. `dragPathMime` carries the list of paths being
              // dragged (array, JSON-encoded). If the dragged row is part
              // of the current selection, drag the WHOLE selection as a
              // unit — dropping anywhere moves all selected rows. If the
              // dragged row is NOT selected, it's an individual drag and
              // the selection is untouched.
              rowSelection={{
                selectedRowKeys: selectedNames,
                onChange: (keys) => setSelectedNames(keys as string[]),
                columnWidth: 40,
              }}
              onRow={(entry) => ({
                draggable: true,
                onDragStart: (e) => {
                  if (!currentPath) return;
                  const p = joinPath(currentPath, entry.name);
                  // If this row is part of the current selection, drag
                  // the whole selection. Otherwise drag just this row.
                  const paths = selectedNames.includes(entry.name)
                    ? selectedNames.map((n) => joinPath(currentPath, n))
                    : [p];
                  setDraggedPath(p);
                  e.dataTransfer.setData(dragPathMime, JSON.stringify(paths));
                  e.dataTransfer.effectAllowed = "move";
                },
                onDragOver: (e) => {
                  if (!entry.is_dir) return;
                  if (!e.dataTransfer.types.includes(dragPathMime)) return;
                  e.preventDefault();
                  e.dataTransfer.dropEffect = "move";
                },
                onDrop: (e) => {
                  if (!entry.is_dir || !currentPath) return;
                  const raw = e.dataTransfer.getData(dragPathMime);
                  if (!raw) return;
                  e.preventDefault();
                  e.stopPropagation();
                  const destDir = joinPath(currentPath, entry.name);
                  const paths = parseDragPayload(raw);
                  if (paths.length === 1) {
                    void handleMove(paths[0], destDir);
                  } else if (paths.length > 1) {
                    void runBulk("Moved", paths, (p) => filesMove(p, destDir));
                  }
                },
                onDragEnd: () => setDraggedPath(null),
                style:
                  draggedPath && draggedPath === joinPath(currentPath || "", entry.name)
                    ? { opacity: 0.4 }
                    : undefined,
              })}
            />
          </Spin>
        </div>
      </div>

      <Modal
        title="New Folder"
        open={mkdirOpen}
        onOk={() => void submitMkdir()}
        onCancel={() => setMkdirOpen(false)}
        okText="Create"
      >
        <Input
          value={mkdirName}
          onChange={(e) => setMkdirName(e.target.value)}
          placeholder="folder-name"
          autoFocus
          onPressEnter={() => void submitMkdir()}
        />
      </Modal>

      <Modal
        title={`Rename ${renameTarget?.name ?? ""}`}
        open={renameOpen}
        onOk={() => void submitRename()}
        onCancel={() => setRenameOpen(false)}
        okText="Rename"
      >
        <Input
          value={renameNewName}
          onChange={(e) => setRenameNewName(e.target.value)}
          autoFocus
          onPressEnter={() => void submitRename()}
        />
      </Modal>

      <Modal
        title={previewPath}
        open={previewOpen}
        onCancel={() => setPreviewOpen(false)}
        width="min(900px, 90vw)"
        footer={null}
      >
        <Spin spinning={previewLoading}>
          <pre
            style={{
              maxHeight: "60vh",
              overflow: "auto",
              background: "#fafafa",
              padding: 12,
              borderRadius: 4,
              fontSize: 12,
              whiteSpace: "pre-wrap",
              wordBreak: "break-all",
            }}
          >
            {previewContent}
          </pre>
        </Spin>
      </Modal>

      <Modal
        title={`Move ${selectedPaths.length} item${selectedPaths.length === 1 ? "" : "s"}`}
        open={bulkMoveOpen}
        onOk={() => void handleBulkMove()}
        onCancel={() => setBulkMoveOpen(false)}
        okText="Move"
      >
        <p style={{ marginBottom: 8 }}>
          Destination folder (absolute path inside your home):
        </p>
        <Input
          value={bulkMoveDest}
          onChange={(e) => setBulkMoveDest(e.target.value)}
          placeholder={rootPath || "/home/..."}
          autoFocus
        />
      </Modal>

      <Modal
        title={`Change permissions on ${selectedPaths.length} item${selectedPaths.length === 1 ? "" : "s"}`}
        open={bulkChmodOpen}
        onOk={() => void handleBulkChmod()}
        onCancel={() => setBulkChmodOpen(false)}
        okText="Apply"
      >
        <ChmodEditor value={bulkChmodMode} onChange={setBulkChmodMode} />
      </Modal>
    </div>
  );
};

// ChmodEditor — two-way bound rwx-checkbox grid + octal text input.
// Typing in the box updates the checkboxes when the input parses, and
// toggling checkboxes rewrites the box. Accepts only 3- or 4-digit octal
// strings; anything else leaves the checkboxes where they were.
function ChmodEditor({
  value,
  onChange,
}: {
  value: string;
  onChange: (next: string) => void;
}) {
  const bits = parseOctalToBits(value);
  const setBit = (i: number, b: boolean) => {
    const next = [...bits];
    next[i] = b;
    onChange(bitsToOctal(next));
  };
  const labels = ["Owner", "Group", "Other"];
  return (
    <div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "80px repeat(3, auto)",
          gap: 8,
          alignItems: "center",
          marginBottom: 12,
        }}
      >
        <div />
        <Text strong>Read</Text>
        <Text strong>Write</Text>
        <Text strong>Exec</Text>
        {labels.map((lab, row) => (
          <Row key={lab} lab={lab} row={row} bits={bits} setBit={setBit} />
        ))}
      </div>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="0644"
        style={{ fontFamily: "monospace" }}
        maxLength={4}
      />
    </div>
  );
}

function Row({
  lab,
  row,
  bits,
  setBit,
}: {
  lab: string;
  row: number;
  bits: boolean[];
  setBit: (i: number, b: boolean) => void;
}) {
  // row=0 → owner bits at 0..2; row=1 → group at 3..5; row=2 → other at 6..8
  const base = row * 3;
  return (
    <>
      <Text>{lab}</Text>
      {[0, 1, 2].map((col) => (
        <input
          key={col}
          type="checkbox"
          checked={bits[base + col] || false}
          onChange={(e) => setBit(base + col, e.target.checked)}
        />
      ))}
    </>
  );
}

// parseOctalToBits: "0755" → [r,w,x, r,-,x, r,-,x] flattened as 9 booleans.
// Returns all-false on an unparseable string so the editor stays open.
function parseOctalToBits(s: string): boolean[] {
  const trimmed = s.trim().replace(/^0o/i, "");
  if (!/^[0-7]{3,4}$/.test(trimmed)) return new Array(9).fill(false);
  // Use the low 3 digits — the setuid/setgid/sticky digit is ignored by
  // this editor's UI (it's in the Advanced octal box).
  const rwx = trimmed.slice(-3);
  const bits: boolean[] = [];
  for (const ch of rwx) {
    const n = parseInt(ch, 8);
    bits.push((n & 4) !== 0, (n & 2) !== 0, (n & 1) !== 0);
  }
  return bits;
}

function bitsToOctal(bits: boolean[]): string {
  let out = "0";
  for (let i = 0; i < 3; i++) {
    let d = 0;
    if (bits[i * 3 + 0]) d |= 4;
    if (bits[i * 3 + 1]) d |= 2;
    if (bits[i * 3 + 2]) d |= 1;
    out += d.toString(8);
  }
  return out;
}

// updateTreeNode replaces the children of a tree node at `path` (immutably).
function updateTreeNode(nodes: TreeNode[], path: string, children: TreeNode[]): TreeNode[] {
  return nodes.map((n) => {
    if (n.path === path) return { ...n, children };
    if (n.children) {
      return { ...n, children: updateTreeNode(n.children as TreeNode[], path, children) };
    }
    return n;
  });
}
