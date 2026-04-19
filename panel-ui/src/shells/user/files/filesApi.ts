// filesApi.ts — typed wrappers around the /api/v1/files endpoints.
import { apiClient } from "../../../apiClient";

export type FileEntry = {
  name: string;
  is_dir: boolean;
  size: number;
  mode: string;
  mod_time: string;
  is_symlink: boolean;
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

export async function filesDelete(path: string, recursive = false): Promise<void> {
  await apiClient.delete("/files", {
    params: { path, ...(recursive ? { recursive: "true" } : {}) },
  });
}
