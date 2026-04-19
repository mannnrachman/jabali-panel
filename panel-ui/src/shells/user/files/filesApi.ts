// filesApi.ts — typed wrappers around the /api/v1/files endpoints.
import { apiClient } from "../../../apiClient";

export type FileEntry = {
  name: string;
  is_dir: boolean;
  size: number;
  mode: string;
  mod_time: string;
  is_symlink: boolean;
  // Only meaningful for is_dir entries; absent/false for files. Drives
  // the tree's chevron visibility — a folder with no subfolders is
  // rendered as a leaf (no expand arrow).
  has_subdirs?: boolean;
};

export type FileListResponse = {
  path: string;
  entries: FileEntry[];
};

export type FilePreviewResponse = {
  path: string;
  size: number;
  content: string;
};

export async function filesHome(): Promise<{ path: string }> {
  const r = await apiClient.get<{ path: string }>("/files/home");
  return r.data;
}

export async function filesList(path: string): Promise<FileListResponse> {
  const r = await apiClient.get<FileListResponse>("/files", { params: { path } });
  return r.data;
}

export async function filesTree(path: string): Promise<FileListResponse> {
  const r = await apiClient.get<FileListResponse>("/files/tree", { params: { path } });
  return r.data;
}

export async function filesPreview(path: string): Promise<FilePreviewResponse> {
  const r = await apiClient.get<FilePreviewResponse>("/files/preview", {
    params: { path },
  });
  return r.data;
}

export function filesDownloadURL(path: string): string {
  return `/api/v1/files/download?path=${encodeURIComponent(path)}`;
}

export async function filesUpload(dirPath: string, file: File): Promise<void> {
  const fd = new FormData();
  fd.append("file", file);
  await apiClient.post(`/files/upload?path=${encodeURIComponent(dirPath)}`, fd, {
    headers: { "Content-Type": "multipart/form-data" },
  });
}

// filesUploadChunked — chunked upload for files > 100 MB. Sends the
// file as N sequential POSTs of `chunkSize` bytes each, the last one
// flagged `final=1` so the backend finalises (moves /tmp into scope).
// `onProgress` is called with a 0..1 fraction after each chunk.
export async function filesUploadChunked(
  dirPath: string,
  file: File,
  chunkSize = 10 * 1024 * 1024,
  onProgress?: (frac: number) => void,
): Promise<void> {
  const uploadId =
    typeof crypto !== "undefined" && "randomUUID" in crypto
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const totalChunks = Math.max(1, Math.ceil(file.size / chunkSize));
  for (let i = 0; i < totalChunks; i++) {
    const start = i * chunkSize;
    const end = Math.min(start + chunkSize, file.size);
    const blob = file.slice(start, end);
    const params = new URLSearchParams({
      upload_id: uploadId,
      offset: String(start),
      path: dirPath,
      name: file.name,
      ...(i === totalChunks - 1 ? { final: "1" } : {}),
    });
    await apiClient.post(`/files/upload-chunk?${params.toString()}`, blob, {
      headers: { "Content-Type": "application/octet-stream" },
    });
    if (onProgress) onProgress((i + 1) / totalChunks);
  }
}

// filesWrite overwrites the content of an existing file (or creates it
// if missing) with the given UTF-8 string. Powers the Monaco editor's
// Save action — binary-safe reads/writes are a Phase-3 concern.
export async function filesWrite(path: string, content: string): Promise<void> {
  await apiClient.post("/files/write", { path, content });
}

export async function filesMkdir(path: string): Promise<void> {
  await apiClient.post("/files/mkdir", { path });
}

export async function filesRename(path: string, newName: string): Promise<void> {
  await apiClient.post("/files/rename", { path, new_name: newName });
}

// filesMove relocates a file or directory into a different parent
// directory. Distinct from rename (same-parent only). Powers the
// drag-and-drop flow — dragging a row onto a folder row moves the
// source into that folder, preserving the basename.
export async function filesMove(path: string, destDir: string): Promise<void> {
  await apiClient.post("/files/move", { path, dest_dir: destDir });
}

// filesChmod sets Unix permission bits on a single file or directory.
// `mode` is a 3- or 4-digit octal string ("755", "0644", "1777"); the
// agent parses + masks to the low 12 bits. Bulk chmod from the UI
// loops this per entry so per-item failures surface individually.
export async function filesChmod(path: string, mode: string): Promise<void> {
  await apiClient.post("/files/chmod", { path, mode });
}

// filesCopy recursively copies a scoped path into a different parent
// directory, preserving mode and symlink targets. Basename preserved
// server-side — the caller sends the destination *folder*, not the
// destination path.
export async function filesCopy(path: string, destDir: string): Promise<void> {
  await apiClient.post("/files/copy", { path, dest_dir: destDir });
}

// filesArchive posts the selection and streams back a tar.gz download.
// One request = one archive — the backend creates a scratch file, streams
// it out, and unlinks as part of the same round-trip. Returns the Blob
// so the caller can trigger a save-as on the user's machine.
export async function filesArchive(paths: string[]): Promise<Blob> {
  const r = await apiClient.post<Blob>(
    "/files/archive",
    { paths },
    { responseType: "blob" },
  );
  return r.data;
}

export async function filesDelete(path: string, recursive = false): Promise<void> {
  await apiClient.delete("/files", {
    params: { path, ...(recursive ? { recursive: "true" } : {}) },
  });
}
